package devin

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRedirectURL(t *testing.T) {
	for _, tc := range []struct {
		name, location string
		status         int
		wantErr        bool
	}{
		{"signed", "https://files.example/file?signature=secret", 307, false},
		{"missing", "", 307, true},
		{"relative", "/file", 307, true},
		{"insecure", "http://files.example/file", 307, true},
		{"userinfo", "https://user:pass@files.example/file", 307, true},
		{"unexpected", "", 200, true},
		{"forbidden", "", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			c, _ := NewClient("secret", WithHTTPClient(&http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
				count++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {tc.location}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}))
			got, _, err := c.RedirectURL(context.Background(), "/attachment", nil)
			if (err != nil) != tc.wantErr || count != 1 {
				t.Fatalf("got %q, %v, %d calls", got, err, count)
			}
			if !tc.wantErr && got != tc.location {
				t.Fatalf("URL = %q", got)
			}
		})
	}
}
