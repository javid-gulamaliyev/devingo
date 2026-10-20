package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	defaultBaseURL          = "https://api.devin.ai"
	defaultMaxResponseBytes = int64(32 << 20)
	maxErrorBodyBytes       = int64(64 << 10)
	defaultUserAgent        = "devin-go/1.0"
)

// Client is a concurrency-safe HTTP client for the Devin API.
type Client struct {
	token            string
	baseURL          *url.URL
	httpClient       *http.Client
	userAgent        string
	retryPolicy      RetryPolicy
	maxResponseBytes int64
}

// Option configures a Client during construction.
type Option func(*Client) error

// RetryPolicy enables retries for bodyless GET and HEAD requests only.
// MaxAttempts includes the first request. Zero means one attempt. Zero delays
// select defaults of 250 ms and 5 s respectively.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// Response holds HTTP metadata. Its embedded response Body has been closed.
type Response struct {
	*http.Response
	RequestID  string
	RetryAfter time.Duration
}

// NewClient constructs an API client with a 60-second HTTP timeout, a 32 MiB
// response limit, and no retries. The bearer token must be nonempty.
func NewClient(token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("devin: invalid API token")
	}
	base, _ := url.Parse(defaultBaseURL)
	c := &Client{
		token:            token,
		baseURL:          base,
		httpClient:       &http.Client{Timeout: 60 * time.Second},
		userAgent:        defaultUserAgent,
		maxResponseBytes: defaultMaxResponseBytes,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("devin: nil client option")
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.retryPolicy.MaxAttempts == 0 {
		c.retryPolicy.MaxAttempts = 1
	}
	if c.retryPolicy.MaxAttempts < 0 || c.retryPolicy.BaseDelay < 0 || c.retryPolicy.MaxDelay < 0 {
		return nil, errors.New("devin: invalid retry policy")
	}
	if c.retryPolicy.BaseDelay == 0 {
		c.retryPolicy.BaseDelay = 250 * time.Millisecond
	}
	if c.retryPolicy.MaxDelay == 0 {
		c.retryPolicy.MaxDelay = 5 * time.Second
	}
	if c.retryPolicy.MaxDelay < c.retryPolicy.BaseDelay {
		return nil, errors.New("devin: maximum retry delay is below base delay")
	}
	// Copy the caller's client. Redirects are refused even when it has its own
	// redirect policy, so Authorization can never be sent to a redirect target.
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.httpClient = &httpClient
	return c, nil
}

// WithHTTPClient uses the supplied client's transport and timeout. It is
// copied at construction; later changes to the original are not observed.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) error {
		if client == nil {
			return errors.New("devin: nil HTTP client")
		}
		c.httpClient = client
		return nil
	}
}

// WithBaseURL sets the API origin and optional path prefix.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		base, err := url.Parse(raw)
		if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.RawFragment != "" {
			return errors.New("devin: invalid base URL")
		}
		c.baseURL = base
		return nil
	}
}

// WithUserAgent replaces the default User-Agent header.
func WithUserAgent(value string) Option {
	return func(c *Client) error {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return errors.New("devin: invalid user agent")
		}
		c.userAgent = value
		return nil
	}
}

// WithRetryPolicy configures retries for safe, bodyless read requests.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(c *Client) error {
		c.retryPolicy = policy
		return nil
	}
}

// WithMaxResponseBytes sets the maximum successful response body size. Zero
// disables the limit. Error response bodies remain capped at 64 KiB.
func WithMaxResponseBytes(limit int64) Option {
	return func(c *Client) error {
		if limit < 0 {
			return errors.New("devin: negative response size limit")
		}
		c.maxResponseBytes = limit
		return nil
	}
}

// Do sends a request to a root-relative API path with a JSON-encoded body.
// When out is non-nil, exactly one JSON value is decoded into it. An empty
// successful response is accepted. Non-2xx statuses return an *APIError and a
// non-nil *Response.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) (*Response, error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("devin: encode request body: %w", err)
		}
		reader = bytes.NewReader(payload)
		contentType = "application/json"
	}
	return c.do(ctx, method, path, query, "application/json", contentType, reader, out, body == nil)
}

// DoRaw sends an unencoded body, usually multipart data. The body is never
// retried because an io.Reader may be non-replayable. If out implements
// io.Writer, response bytes are streamed into it; otherwise out is decoded
// as JSON. contentType may be empty when no Content-Type header is needed.
func (c *Client) DoRaw(ctx context.Context, method, path string, query url.Values, contentType string, body io.Reader, out any) (*Response, error) {
	if strings.ContainsAny(contentType, "\r\n") {
		return nil, errors.New("devin: invalid content type")
	}
	return c.do(ctx, method, path, query, "*/*", contentType, body, out, false)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, accept, contentType string, body io.Reader, out any, retry bool) (*Response, error) {
	if c == nil {
		return nil, errors.New("devin: nil client")
	}
	if ctx == nil {
		return nil, errors.New("devin: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := c.requestURL(path, query)
	if err != nil {
		return nil, err
	}
	attempts := 1
	if retry && (method == http.MethodGet || method == http.MethodHead) {
		attempts = c.retryPolicy.MaxAttempts
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
		if err != nil {
			return nil, fmt.Errorf("devin: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", c.userAgent)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < attempts && ctx.Err() == nil && retryableTransportError(err) {
				if err := waitForRetry(ctx, c.retryDelay(attempt, nil)); err != nil {
					return nil, err
				}
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("devin: %s %s: %w", method, path, err)
		}
		response := &Response{
			Response:   resp,
			RequestID:  resp.Header.Get("X-Request-ID"),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
		if response.RequestID == "" {
			response.RequestID = resp.Header.Get("X-Devin-Request-ID")
		}
		if attempt < attempts && retryableStatus(resp.StatusCode) {
			delay := c.retryDelay(attempt, resp)
			_ = resp.Body.Close()
			if err := waitForRetry(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		return c.readResponse(response, method, path, out)
	}
	return nil, errors.New("devin: retry attempts exhausted")
}

func (c *Client) requestURL(path string, query url.Values) (*url.URL, error) {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#\\") {
		return nil, errors.New("devin: invalid request path")
	}
	for _, segment := range strings.Split(path, "/") {
		// A proxy may decode an escaped segment more than once.
		for first := true; ; first = false {
			decoded, err := url.PathUnescape(segment)
			if err != nil {
				if !first {
					break // A literal percent sign in an ID is allowed.
				}
				return nil, errors.New("devin: invalid request path")
			}
			if strings.Contains(decoded, "\\") {
				return nil, errors.New("devin: invalid request path")
			}
			for _, r := range decoded {
				if unicode.IsControl(r) {
					return nil, errors.New("devin: invalid request path")
				}
			}
			for _, component := range strings.Split(decoded, "/") {
				if component == "." || component == ".." {
					return nil, errors.New("devin: request path traversal")
				}
			}
			if decoded == segment {
				break
			}
			segment = decoded
		}
	}
	target := *c.baseURL
	prefix := strings.TrimSuffix(target.EscapedPath(), "/")
	escapedPath := prefix + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, errors.New("devin: invalid request path")
	}
	target.Path = decodedPath
	target.RawPath = escapedPath
	target.RawQuery = query.Encode()
	return &target, nil
}

func (c *Client) readResponse(response *Response, method, path string, out any) (*Response, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		if err != nil {
			return response, fmt.Errorf("devin: read error response: %w", err)
		}
		truncated := int64(len(body)) > maxErrorBodyBytes
		if truncated {
			body = body[:maxErrorBodyBytes]
		}
		return response, decodeAPIError(response.StatusCode, method, path, response.RequestID, body, truncated)
	}
	if writer, ok := out.(io.Writer); ok {
		if c.maxResponseBytes == 0 {
			if _, err := io.Copy(writer, response.Body); err != nil {
				return response, fmt.Errorf("devin: copy response: %w", err)
			}
			return response, nil
		}
		if _, err := io.Copy(writer, io.LimitReader(response.Body, c.maxResponseBytes)); err != nil {
			return response, fmt.Errorf("devin: copy response: %w", err)
		}
		var extra [1]byte
		n, err := io.ReadFull(response.Body, extra[:])
		if n == 0 && err == io.EOF {
			return response, nil
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return response, fmt.Errorf("devin: read response: %w", err)
		}
		return response, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, c.maxResponseBytes)
	}
	var source io.Reader = response.Body
	if c.maxResponseBytes > 0 {
		limit := c.maxResponseBytes
		if limit < int64(^uint64(0)>>1) {
			limit++
		}
		source = io.LimitReader(source, limit)
	}
	body, err := io.ReadAll(source)
	if err != nil {
		return response, fmt.Errorf("devin: read response: %w", err)
	}
	if c.maxResponseBytes > 0 && int64(len(body)) > c.maxResponseBytes {
		return response, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, c.maxResponseBytes)
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return response, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(out); err != nil {
		return response, fmt.Errorf("devin: decode response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return response, errors.New("devin: response contains multiple JSON values")
		}
		return response, fmt.Errorf("devin: trailing response data: %w", err)
	}
	return response, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryableTransportError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	delay, _ := parseRetryAfterValue(value, now)
	return delay
}

func parseRetryAfterValue(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0, true
		}
		if seconds > int64(^uint64(0)>>1)/int64(time.Second) {
			return time.Duration(^uint64(0) >> 1), true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if then, err := http.ParseTime(value); err == nil {
		if delay := then.Sub(now); delay > 0 {
			return delay, true
		}
		return 0, true
	}
	return 0, false
}

func (c *Client) retryDelay(attempt int, response *http.Response) time.Duration {
	if response != nil {
		if delay, ok := parseRetryAfterValue(response.Header.Get("Retry-After"), time.Now()); ok {
			if delay > c.retryPolicy.MaxDelay {
				return c.retryPolicy.MaxDelay
			}
			return delay
		}
	}
	ceiling := c.retryPolicy.BaseDelay
	for i := 1; i < attempt && ceiling < c.retryPolicy.MaxDelay; i++ {
		if ceiling > c.retryPolicy.MaxDelay/2 {
			ceiling = c.retryPolicy.MaxDelay
		} else {
			ceiling *= 2
		}
	}
	if ceiling > c.retryPolicy.MaxDelay {
		ceiling = c.retryPolicy.MaxDelay
	}
	return ceiling/2 + time.Duration(rand.Int63n(int64(ceiling-ceiling/2)+1))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
