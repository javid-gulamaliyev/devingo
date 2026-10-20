package devin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type testServer struct {
	URL    string
	client *http.Client
}

func newTestServer(handler http.Handler) *testServer {
	return &testServer{
		URL: "https://test.devin.invalid",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			return recorder.Result(), nil
		})},
	}
}

func testClient(t *testing.T, server *testServer, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithBaseURL(server.URL), WithHTTPClient(server.client)}, opts...)
	c, err := NewClient("test-token", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientRequestAndResponse(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/prefix/v1/sessions/a%2Fb" || r.URL.Query().Get("first") != "20" {
			t.Errorf("request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "custom/2" {
			t.Errorf("headers: %#v", r.Header)
		}
		data, _ := io.ReadAll(r.Body)
		if string(data) != `{"name":"foo"}` {
			t.Errorf("body: %s", data)
		}
		w.Header().Set("X-Request-ID", "req-123")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"one","future_field":true}`)
	}))
	c := testClient(t, server, WithBaseURL(server.URL+"/prefix/"), WithUserAgent("custom/2"))
	var output struct {
		ID string `json:"id"`
	}
	resp, err := c.Do(context.Background(), http.MethodPost, "/v1/sessions/a%2Fb", url.Values{"first": {"20"}}, map[string]string{"name": "foo"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.ID != "one" || resp.RequestID != "req-123" || resp.StatusCode != http.StatusOK {
		t.Fatalf("response = %+v, output = %+v", resp, output)
	}
}

func TestClientRejectsUnsafePathsBeforeRequest(t *testing.T) {
	var count atomic.Int32
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	c := testClient(t, server)
	for _, path := range []string{"", "v1/foo", "https://evil.test/x", "//evil.test/x", "/a?token=secret", "/a#fragment", "/a/../b", "/a/%2e%2e/b", "/a/%252e%252e/b", "/a/%2F..%2Fb", "/a/%5c/b", "/a/%00/b", "/a/%0d%0a/b", "/a/\n/b", "/bad%zz"} {
		t.Run(path, func(t *testing.T) {
			if _, err := c.Do(context.Background(), http.MethodGet, path, nil, nil, nil); err == nil {
				t.Fatal("expected path validation error")
			}
		})
	}
	if count.Load() != 0 {
		t.Fatalf("sent %d unsafe requests", count.Load())
	}
}

func TestClientAPIError(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "request-42")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"type":"https://example.test/problem","title":"Invalid","message":"secret-value","detail":[{"loc":["body","title"],"msg":"required","type":"missing"}]}`)
	}))
	c := testClient(t, server)
	resp, err := c.Do(context.Background(), http.MethodPost, "/v1/sessions", nil, struct{}{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if resp == nil || resp.RequestID != "request-42" || resp.RetryAfter != 2*time.Second || apiErr.StatusCode != 422 || apiErr.RequestID != "request-42" || apiErr.Message != "secret-value" || apiErr.Type != "https://example.test/problem" || apiErr.Title != "Invalid" || len(apiErr.Details) != 1 {
		t.Fatalf("response = %+v, api error = %+v", resp, apiErr)
	}
	if strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "required") {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func TestClientErrorBodyIsBounded(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("x", int(maxErrorBodyBytes)+100))
	}))
	c := testClient(t, server, WithMaxResponseBytes(0))
	_, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Truncated || len(apiErr.Body) != int(maxErrorBodyBytes) {
		t.Fatalf("unbounded API error: %+v", apiErr)
	}
}

func TestClientResponseLimitAndJSONValidation(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = io.WriteString(w, `{"data":"1234567890"}`)
		case "/trailing":
			_, _ = io.WriteString(w, `{"ok":true}{"ok":false}`)
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	c := testClient(t, server, WithMaxResponseBytes(8))
	if _, err := c.Do(context.Background(), http.MethodGet, "/large", nil, nil, nil); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected response size error, got %v", err)
	}
	c = testClient(t, server)
	var output map[string]any
	if _, err := c.Do(context.Background(), http.MethodGet, "/trailing", nil, nil, &output); err == nil {
		t.Fatal("accepted multiple JSON values")
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/empty", nil, nil, &output); err != nil {
		t.Fatalf("empty response: %v", err)
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/trailing", nil, nil, nil); err != nil {
		t.Fatalf("nil output should permit an opaque body: %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	redirect := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	original := redirect.client
	c := testClient(t, redirect, WithHTTPClient(original))
	resp, err := c.Do(context.Background(), http.MethodGet, "/redirect", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || resp.StatusCode != http.StatusFound || leaked.Load() || original.CheckRedirect != nil {
		t.Fatalf("redirect result: response=%+v error=%v leaked=%t", resp, err, leaked.Load())
	}
}

func TestClientRetriesSafeRequestsOnly(t *testing.T) {
	var gets, posts atomic.Int32
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if gets.Add(1) < 3 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	c := testClient(t, server, WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))
	var result struct {
		OK bool `json:"ok"`
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, &result); err != nil || !result.OK || gets.Load() != 3 {
		t.Fatalf("GET: result=%+v err=%v attempts=%d", result, err, gets.Load())
	}
	if _, err := c.Do(context.Background(), http.MethodPost, "/x", nil, map[string]bool{"x": true}, nil); err == nil || posts.Load() != 1 {
		t.Fatalf("POST: err=%v attempts=%d", err, posts.Load())
	}
}

func TestClientCancellationStopsRetryWait(t *testing.T) {
	var calls atomic.Int32
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	c := testClient(t, server, WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 3 * time.Second}))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, http.MethodGet, "/x", nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("err=%v attempts=%d", err, calls.Load())
	}
}

func TestClientDoRawUploadAndDownload(t *testing.T) {
	var calls atomic.Int32
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Accept") != "*/*" {
			t.Errorf("accept: %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "multipart/form-data; boundary=abc" {
			t.Errorf("content type: %q", r.Header.Get("Content-Type"))
		}
		payload, _ := io.ReadAll(r.Body)
		if string(payload) != "--abc\r\nattachment\r\n--abc--\r\n" {
			t.Errorf("upload payload: %q", payload)
		}
		_, _ = io.WriteString(w, "downloaded")
	}))
	c := testClient(t, server, WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))
	var output bytes.Buffer
	_, err := c.DoRaw(context.Background(), http.MethodPost, "/upload", nil, "multipart/form-data; boundary=abc", strings.NewReader("--abc\r\nattachment\r\n--abc--\r\n"), &output)
	if err != nil || output.String() != "downloaded" || calls.Load() != 1 {
		t.Fatalf("output=%q err=%v calls=%d", output.String(), err, calls.Load())
	}
}

func TestClientDoRawWriterLimitAndNoRetry(t *testing.T) {
	var calls atomic.Int32
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "123456")
	}))
	c := testClient(t, server, WithMaxResponseBytes(5), WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))
	var output bytes.Buffer
	_, err := c.DoRaw(context.Background(), http.MethodGet, "/download", nil, "", nil, &output)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || calls.Load() != 1 {
		t.Fatalf("expected single failed attempt, err=%v calls=%d", err, calls.Load())
	}
	_, err = c.DoRaw(context.Background(), http.MethodGet, "/download", nil, "", nil, &output)
	if !errors.Is(err, ErrResponseTooLarge) || output.String() != "12345" || calls.Load() != 2 {
		t.Fatalf("writer limit: output=%q err=%v calls=%d", output.String(), err, calls.Load())
	}
}

type oneTransientEmptyReadBody struct {
	data  string
	read  bool
	empty bool
}

func (b *oneTransientEmptyReadBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		return copy(p, b.data), nil
	}
	if !b.empty {
		b.empty = true
		return 0, nil
	}
	return 0, io.EOF
}

func (b *oneTransientEmptyReadBody) Close() error { return nil }

func TestClientWriterLimitAcceptsTransientEmptyReadAtBoundary(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &oneTransientEmptyReadBody{data: "12345"},
		}, nil
	})}
	c := testClient(t, newTestServer(http.NotFoundHandler()), WithHTTPClient(httpClient), WithMaxResponseBytes(5))
	var output bytes.Buffer
	if _, err := c.DoRaw(context.Background(), http.MethodGet, "/download", nil, "", nil, &output); err != nil {
		t.Fatalf("exact-limit response was rejected: %v", err)
	}
	if output.String() != "12345" {
		t.Fatalf("output = %q, want exact response body", output.String())
	}
}

func TestClientRetriesTransientTransportError(t *testing.T) {
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, io.EOF
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	c, err := NewClient("token", WithHTTPClient(httpClient), WithRetryPolicy(RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, &result); err != nil || !result.OK || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestClientOptionsValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		opt  Option
	}{
		{"nil HTTP client", WithHTTPClient(nil)},
		{"bad base", WithBaseURL("//example.com")},
		{"base query", WithBaseURL("https://example.com/api?x=y")},
		{"base credentials", WithBaseURL("https://user:pass@example.com")},
		{"negative limit", WithMaxResponseBytes(-1)},
		{"negative attempts", WithRetryPolicy(RetryPolicy{MaxAttempts: -1})},
		{"bad user agent", WithUserAgent("a\nb")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient("token", test.opt); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := NewClient(" "); err == nil {
		t.Fatal("accepted empty token")
	}
}
