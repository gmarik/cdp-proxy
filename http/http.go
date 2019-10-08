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
					http.Hijacker
					http.ResponseWriter
				}{
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
		Request:       r,
		StatusCode:    w.status,
		Status:        http.StatusText(w.status),
		ContentLength: w.contentLength,
		// TODO:
		Proto:      r.Proto,
		ProtoMajor: r.ProtoMajor,
		ProtoMinor: r.ProtoMinor,
		// TODO: +1.13
		Header: w.headerWriter.Header().Clone(),
		// TODO
		// BODY
	}
}

func (w *responseWriter) Write(p []byte) (n int, err error) {
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
