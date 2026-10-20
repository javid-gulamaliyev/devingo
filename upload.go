package devin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
)

// Upload describes a file to upload. Reader is consumed once and is not closed
// by the client. Filename must be nonempty. ContentType defaults to
// application/octet-stream.
type Upload struct {
	Reader      io.Reader
	Filename    string
	ContentType string
}

// Upload streams a multipart request without buffering the file or starting
// background goroutines. fields contains additional text form fields.
// Uploads are never retried. The caller owns the file reader and must arrange
// for a blocking reader to unblock when the request's context is canceled.
func (c *Client) Upload(ctx context.Context, method, path string, query url.Values, fields map[string]string, fileField string, file *Upload, out any) (*Response, error) {
	if file == nil || file.Reader == nil || file.Filename == "" || fileField == "" {
		return nil, errors.New("devin: upload requires a reader, filename, and file field")
	}
	if strings.ContainsAny(file.Filename+fileField, "\r\n") {
		return nil, errors.New("devin: invalid multipart filename or field")
	}
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return nil, errors.New("devin: invalid upload content type")
	}
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if strings.ContainsAny(key, "\r\n") {
			return nil, errors.New("devin: invalid multipart field")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := writer.WriteField(key, fields[key]); err != nil {
			return nil, err
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name": fileField, "filename": file.Filename,
	}))
	header.Set("Content-Type", contentType)
	if _, err := writer.CreatePart(header); err != nil {
		return nil, err
	}
	prefix := append([]byte(nil), envelope.Bytes()...)
	envelope.Reset()
	if err := writer.Close(); err != nil {
		return nil, err
	}
	body := io.MultiReader(bytes.NewReader(prefix), file.Reader, bytes.NewReader(envelope.Bytes()))
	return c.DoRaw(ctx, method, path, query, writer.FormDataContentType(), body, out)
}
