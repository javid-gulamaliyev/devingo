package devin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// RedirectURL returns a download endpoint's signed HTTPS URL. It does not fetch
// the URL: use a separate unauthenticated client before the signed URL expires.
func (c *Client) RedirectURL(ctx context.Context, path string, query url.Values) (string, *Response, error) {
	resp, err := c.DoRaw(ctx, http.MethodGet, path, query, "", nil, nil)
	var apiError *APIError
	if !errors.As(err, &apiError) || resp == nil || resp.StatusCode != http.StatusTemporaryRedirect {
		if err == nil {
			err = fmt.Errorf("devin: expected download redirect, got HTTP %d", resp.StatusCode)
		}
		return "", resp, err
	}
	location := resp.Header.Get("Location")
	target, parseErr := url.Parse(location)
	if parseErr != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
		return "", resp, errors.New("devin: download redirect has an invalid HTTPS location")
	}
	return location, resp, nil
}
