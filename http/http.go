package http

import (
	"io"
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
				headerWriter: w,
				Writer: writerFunc(func(p []byte) (int, error) {
					trace.DataReceived(reqID, copySlice(p))
					return w.Write(p)
				}),
			}

			W = func() http.ResponseWriter {
				h, ok := w.(http.Hijacker)
				if !ok {
					return &rw
				}

				return struct {
					http.Flusher
					http.Hijacker
					http.ResponseWriter
				}{
					Flusher:        w.(http.Flusher),
					Hijacker:       h,
					ResponseWriter: &rw,
				}
			}()
		)

		next.ServeHTTP(W, r)

		re := rw.response(r)
		trace.ResponseReceived(reqID, re)
		trace.LoadingFinished(reqID, re)
	})
}

type writerFunc func(p []byte) (int, error)

func (wf writerFunc) Write(p []byte) (int, error) {
	return wf(p)
}

type headerWriter interface {
	WriteHeader(int)
	Header() http.Header
}

type responseWriter struct {
	headerWriter
	io.Writer

	status        int
	contentLength int64
}

func (w *responseWriter) Reset(hw headerWriter, ww io.Writer) {
	w.headerWriter = hw
	w.Writer = w

	w.status = 0
	w.contentLength = 0
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
		Header:        cloneHeader(w.headerWriter.Header()),
	}
}

func (w *responseWriter) Write(p []byte) (n int, err error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	w.contentLength += int64(len(p))
	return w.Writer.Write(p)
}

func (w *responseWriter) WriteHeader(code int) {
	w.headerWriter.WriteHeader(code)
	if w.status > 0 {
		return
	}
	w.status = code
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
