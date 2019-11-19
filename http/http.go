package http

import (
	"bufio"
	"net"
	"net/http"
)

// https://chromedevtools.github.io/devtools-protocol/1-2/Network
type tracer interface {
	RequestWillBeSent(req *http.Request) (reqID string)
	ResponseReceived(reqID string, req *http.Response)
	DataReceived(reqID string, data []byte)
	LoadingFinished(reqID string, req *http.Response)
	LoadingFailed(reqID string, req *http.Request)
}

func Handler(trace tracer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := trace.RequestWillBeSent(r)
		defer func() {
			if perr := recover(); perr != nil {
				trace.LoadingFailed(reqID, r)
				// bubble-up
				panic(perr)
			}
		}()

		var (
			rw = responseWriter{
				ResponseWriter: w,
				tracer:         trace,
				reqID:          reqID,
			}
		)

		next.ServeHTTP(&rw, r)

		re := rw.response(r)

		trace.ResponseReceived(reqID, re)
		trace.LoadingFinished(reqID, re)
	})
}

type responseWriter struct {
	http.ResponseWriter

	status        int
	contentLength int64

	reqID  string
	tracer tracer
}

func (w *responseWriter) response(r *http.Request) *http.Response {
	return &http.Response{
		// NOTE: Request is set only for client requests
		// but it's useful in this case
		Request:       r,
		StatusCode:    w.status,
		Status:        http.StatusText(w.status),
		ContentLength: w.contentLength,
		Proto:         r.Proto,
		ProtoMajor:    r.ProtoMajor,
		ProtoMinor:    r.ProtoMinor,
		Header:        cloneHeader(w.ResponseWriter.Header()),
	}
}

func (w *responseWriter) Write(p []byte) (n int, err error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.contentLength += int64(len(p))
	w.tracer.DataReceived(w.reqID, copySlice(p))
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	if w.status > 0 {
		return
	}
	w.tracer.DataReceived(w.reqID, nil)
	w.status = code
}

type conn struct {
	net.Conn
	tracer tracer
	reqID  string
}

func (c *conn) Write(p []byte) (int, error) {
	c.tracer.DataReceived(c.reqID, copySlice(p))
	return c.Conn.Write(p)
}

func (c *conn) CloseRead() error {
	if cr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return cr.CloseRead()
	}
	return nil
}

func (c *conn) CloseWrite() error {
	if w, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return w.CloseWrite()
	}
	return nil
}
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
		return
	}

	panic("http.Flusher: unavailable")
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		c, buf, err := h.Hijack()
		if err != nil {
			return nil, nil, err
		}
		return &conn{Conn: c, tracer: w.tracer, reqID: w.reqID}, buf, err
	}

	panic("http.Hijacker: unavailable")
}

func copySlice(p []byte) []byte {
	dup := make([]byte, len(p))
	copy(dup, p)
	return dup
}

// https://github.com/golang/go/issues/29915
// backport from go1.13 https://github.com/golang/go/blob/ed7e43085ef2e2c6a1d62785b2d2b343a80039bc/src/net/http/header.go#L81
func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}

	// Find total number of values.
	nv := 0
	for _, vv := range h {
		nv += len(vv)
	}
	sv := make([]string, nv) // shared backing array for headers' values
	h2 := make(http.Header, len(h))
	for k, vv := range h {
		n := copy(sv, vv)
		h2[k] = sv[:n:n]
		sv = sv[n:]
	}
	return h2
}
