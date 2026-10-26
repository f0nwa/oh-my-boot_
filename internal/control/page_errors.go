package control

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
)

type pageErrorKey struct{}

type pageFormKey struct{}

// pageForm returns the form of a failed submission shown again on its page.
func pageForm(r *http.Request) url.Values {
	form, _ := r.Context().Value(pageFormKey{}).(url.Values)
	return form
}

// pageError returns the error of a failed form that is being shown on its page.
// It comes only from the request context, never from the URL.
func pageError(r *http.Request) string {
	message, _ := r.Context().Value(pageErrorKey{}).(string)
	return message
}

// withPageErrors runs a form handler. When it fails with http.Error, the
// handler's page is rendered with the error text and the same status code
// instead of a bare text response.
func withPageErrors(page, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capture := &errorCapture{target: w, header: http.Header{}}
		handler(capture, r)
		if !capture.captured {
			return
		}
		message := strings.TrimSpace(capture.body.String())
		if message == "" {
			message = http.StatusText(capture.status)
		}
		ctx := context.WithValue(r.Context(), pageErrorKey{}, message)
		if r.PostForm != nil {
			ctx = context.WithValue(ctx, pageFormKey{}, r.PostForm)
		}
		get := r.Clone(ctx)
		get.Method = http.MethodGet
		get.Body = http.NoBody
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(capture.status)
		page(&headerlessWriter{w}, get)
	}
}

// errorCapture forwards a handler's response unless it is a plain-text error.
type errorCapture struct {
	target      http.ResponseWriter
	header      http.Header
	status      int
	wroteHeader bool
	captured    bool
	body        bytes.Buffer
}

func (c *errorCapture) Header() http.Header { return c.header }

// Unwrap lets http.ResponseController reach the connection (write deadlines).
func (c *errorCapture) Unwrap() http.ResponseWriter { return c.target }

func (c *errorCapture) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.status = status
	if status >= 400 && strings.HasPrefix(c.header.Get("Content-Type"), "text/plain") {
		c.captured = true
		return
	}
	for key, values := range c.header {
		c.target.Header()[key] = values
	}
	c.target.WriteHeader(status)
}

func (c *errorCapture) Write(data []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	if c.captured {
		if c.body.Len() < 4096 {
			c.body.Write(data[:min(len(data), 4096-c.body.Len())])
		}
		return len(data), nil
	}
	return c.target.Write(data)
}

// headerlessWriter lets the page write its body after the error status was sent.
type headerlessWriter struct{ http.ResponseWriter }

func (h *headerlessWriter) WriteHeader(int) {}
