package idempotency

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
)

// CapturedResponse holds an upstream response for idempotency storage.
type CapturedResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

// ResponseCapturer wraps http.ResponseWriter and records status, headers, and body.
type ResponseCapturer struct {
	http.ResponseWriter
	status      int
	headers     http.Header
	body        bytes.Buffer
	wroteHeader bool
}

// NewResponseCapturer records an upstream response for idempotent completion.
func NewResponseCapturer(w http.ResponseWriter) *ResponseCapturer {
	return &ResponseCapturer{
		ResponseWriter: w,
		status:         http.StatusOK,
		headers:        make(http.Header),
	}
}

func (c *ResponseCapturer) Header() http.Header {
	return c.headers
}

func (c *ResponseCapturer) WriteHeader(code int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.status = code
}

func (c *ResponseCapturer) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(b)
}

func (c *ResponseCapturer) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *ResponseCapturer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := c.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Commit writes the captured response to the underlying writer.
func (c *ResponseCapturer) Commit() CapturedResponse {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	for k, vals := range c.headers {
		for _, v := range vals {
			c.ResponseWriter.Header().Add(k, v)
		}
	}
	c.ResponseWriter.WriteHeader(c.status)
	if c.body.Len() > 0 {
		_, _ = c.ResponseWriter.Write(c.body.Bytes())
	}
	return CapturedResponse{
		StatusCode: c.status,
		Headers:    FilterReplayHeaders(c.headers),
		Body:       append([]byte(nil), c.body.Bytes()...),
	}
}
