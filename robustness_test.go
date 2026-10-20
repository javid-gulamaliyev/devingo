package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentClientReuse(t *testing.T) {
	var count atomic.Int64
	c, err := NewClient("test", WithHTTPClient(&http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		if r.Header.Get("Authorization") != "Bearer test" {
			return nil, fmt.Errorf("missing authentication")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"value":42}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			var result struct{ Value int }
			_, err := c.Do(context.Background(), "GET", "/v3/self", nil, nil, &result)
			if err == nil && result.Value != 42 {
				err = fmt.Errorf("result = %d", result.Value)
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if count.Load() != workers {
		t.Fatalf("request count = %d", count.Load())
	}
}

func TestOptionalRequestBodyIsOmitted(t *testing.T) {
	c, _ := NewClient("test", WithHTTPClient(&http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil || r.Header.Get("Content-Type") != "" {
			t.Error("nil optional request should omit the body and content type")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{}`))}, nil
	})}))
	if _, _, err := c.Organization("org-1").Repositories.IndexSingleRepository(context.Background(), "owner/repo", nil); err != nil {
		t.Fatal(err)
	}
}

func FuzzOptionalJSON(f *testing.F) {
	for _, seed := range []string{`null`, `false`, `0`, `""`, `{}`, `[]`, `{"nested":[1,null,"value"]}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		var value Optional[json.RawMessage]
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var wantCompact, gotCompact bytes.Buffer
		_ = json.Compact(&wantCompact, data)
		_ = json.Compact(&gotCompact, got)
		// encoding/json may normalize HTML-sensitive characters in strings.
		var want, actual any
		decoder := json.NewDecoder(bytes.NewReader(wantCompact.Bytes()))
		decoder.UseNumber()
		_ = decoder.Decode(&want)
		decoder = json.NewDecoder(bytes.NewReader(gotCompact.Bytes()))
		decoder.UseNumber()
		_ = decoder.Decode(&actual)
		wantBytes, _ := json.Marshal(want)
		gotBytes, _ := json.Marshal(actual)
		if !bytes.Equal(wantBytes, gotBytes) {
			t.Fatalf("round trip changed JSON: %s => %s", data, got)
		}
	})
}

func FuzzEscapedPath(f *testing.F) {
	for _, seed := range []string{"id", "a/b", "../x", "%2e%2e", "a?b#c", "日本語", "\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		c, _ := NewClient("test")
		target, err := c.requestURL("/v3/items/"+url.PathEscape(id), nil)
		if err != nil {
			return // Unsafe identifiers may be rejected.
		}
		if target.Scheme != "https" || target.Host != "api.devin.ai" || target.RawQuery != "" || target.Fragment != "" {
			t.Fatalf("identifier changed the target origin: %#v", target)
		}
		if target.EscapedPath() != "/v3/items/"+url.PathEscape(id) {
			t.Fatalf("identifier was not preserved: %q", target.EscapedPath())
		}
	})
}
