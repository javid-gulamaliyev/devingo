package devin

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

type uploadTransport func(*http.Request) (*http.Response, error)

func (fn uploadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestUploadMultipart(t *testing.T) {
	c, err := NewClient("test", WithHTTPClient(&http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for _, want := range []struct{ name, filename, body string }{
			{"a", "", "first"}, {"z", "", "last"}, {"file", `quoted "name".txt`, "file contents"},
		} {
			part, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(part)
			if err != nil || part.FormName() != want.name || part.FileName() != want.filename || string(body) != want.body {
				t.Fatalf("part = %q %q %q, %v; want %+v", part.FormName(), part.FileName(), body, err, want)
			}
		}
		if _, err := mr.NextPart(); err != io.EOF {
			t.Fatalf("end of multipart = %v", err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"url":"uploaded"}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ URL string }
	_, err = c.Upload(context.Background(), "POST", "/files", nil, map[string]string{"z": "last", "a": "first"}, "file",
		&Upload{Reader: strings.NewReader("file contents"), Filename: `quoted "name".txt`}, &result)
	if err != nil || result.URL != "uploaded" {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestUploadValidation(t *testing.T) {
	c, _ := NewClient("test")
	for _, file := range []*Upload{nil, {}, {Reader: strings.NewReader(""), Filename: "x\n"}, {Reader: strings.NewReader(""), Filename: "x", ContentType: "invalid\n"}} {
		if _, err := c.Upload(context.Background(), "POST", "/files", nil, nil, "file", file, nil); err == nil {
			t.Fatalf("accepted invalid upload %+v", file)
		}
	}
}

type failingUploadReader struct{ err error }

func (r failingUploadReader) Read([]byte) (int, error) { return 0, r.err }

func TestUploadReaderError(t *testing.T) {
	sentinel := errors.New("file read failed")
	c, _ := NewClient("test", WithHTTPClient(&http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
		_, err := io.Copy(io.Discard, r.Body)
		return nil, err
	})}))
	_, err := c.Upload(context.Background(), "POST", "/files", nil, nil, "file", &Upload{Reader: failingUploadReader{sentinel}, Filename: "x"}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want file error", err)
	}
}
