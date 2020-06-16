package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

var proxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Host = r.Host
	u.Scheme = "http"

	if _, p, _ := net.SplitHostPort(r.Host); p == "443" {
		u.Scheme = "https"
	}

	log.Printf("[proxy] %s", u.String())

	if r.Method == http.MethodConnect {
		newTunnel(&u).ServeHTTP(w, r)
	} else {
		newForwardProxy(&u).ServeHTTP(w, r)
	}
})

func newTunnel(u *url.URL) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpErr := func(code int, err error) {
			log.Printf("[CONNECT]: error=%v", err)
			http.Error(w, http.StatusText(code), code)
		}
		log.Printf("[CONNECT]: start:%s", u.Host)
		defer log.Printf("[CONNECT]: done: %s", u.Host)

		dconn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			httpErr(http.StatusServiceUnavailable, err)
			return
		}

		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		} else {
			log.Println("http.Flusher: unavailable")
		}

		var sconn net.Conn
		if h, ok := w.(http.Hijacker); !ok {
			httpErr(http.StatusInternalServerError, fmt.Errorf("http.Hijacker: unavailable"))
			return
		} else {
			conn, _, err := h.Hijack()
			if err != nil {
				httpErr(http.StatusServiceUnavailable, err)
				return
			}
			sconn = conn
		}

		type readWriteCloser interface {
			net.Conn
			CloseRead() error
			CloseWrite() error
		}

		var src, dst = sconn.(readWriteCloser), dconn.(readWriteCloser)
		// TODO:
		var done = make(chan struct{})
		go func() {
			n, err := io.Copy(src, dst)
			log.Printf("src<-dst: n=%d error=%v", n, err)
			dst.CloseRead()
			src.CloseWrite()
			done <- struct{}{}
		}()
		go func() {
			n, err := io.Copy(dst, src)
			log.Printf("src->dst: n=%d error=%v", n, err)
			dst.CloseWrite()
			src.CloseRead()
			done <- struct{}{}
		}()
		<-done
		<-done
	})
}

func newForwardProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// TODO:
			req.Host = target.Host
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
		},
		// Transport: loggingTransport(http.DefaultTransport.RoundTrip),
	}
}

type loggingTransport func(*http.Request) (*http.Response, error)

func (lt loggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	bs, _ := httputil.DumpRequestOut(r, false)
	log.Println("REQ:", string(bs))
	re, err := lt(r)
	bs, _ = httputil.DumpResponse(re, false)
	log.Println("RES:", string(bs))
	return re, err
}
