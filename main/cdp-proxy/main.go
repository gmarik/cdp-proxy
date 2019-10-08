package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"time"

	"golang.org/x/sys/unix"

	httpx "github.com/gmarik/cdp-proxy/http"
	"github.com/gmarik/cdp-proxy/main/cdp-proxy/httpcdp"
)

var (
	HTTP_CDP_Addr   = "localhost:9229"
	HTTP_Proxy_Addr = "localhost:8080"
)

func main() {
	flag.StringVar(&HTTP_CDP_Addr, "http-cdp-addr", HTTP_CDP_Addr, "chrome devtools protocol listener address")
	flag.StringVar(&HTTP_Proxy_Addr, "http-proxy-addr", HTTP_Proxy_Addr, "HTTP proxy listener address")
	flag.Parse()

	var eb = httpcdp.NewEventBus()

	go func() {
		log.Printf("http.devtools: ListenAndServe.address=%q", HTTP_CDP_Addr)
		if err := http.ListenAndServe(HTTP_CDP_Addr, httpcdp.Devtools(eb)); err != nil {
			log.Fatalf("http.devtools: ListenAndServe.error=%q", err)
		}
	}()

	go func() {
		log.Printf("http.proxy: ListenAndServe.address=%q", HTTP_Proxy_Addr)
		if err := http.ListenAndServe(HTTP_Proxy_Addr, httpx.Handler(eb, proxy)); err != nil {
			log.Fatalf("http.proxy: ListenAndServe.error=%q", err)
		}
	}()

	sigc := make(chan os.Signal)
	signal.Notify(sigc, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(sigc)

	log.Printf("os: signal=%v", <-sigc)
}

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
		newReverseProxy(&u).ServeHTTP(w, r)
	}
})

func newTunnel(u *url.URL) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpErr := func(code int, err error) {
			log.Printf("[CONNECT]: error=%v", err)
			http.Error(w, http.StatusText(code), code)
		}
		defer log.Printf("[CONNECT]: %s: done", u.Host)
		log.Printf("[CONNECT]: %s", u.Host)

		dconn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			httpErr(http.StatusServiceUnavailable, err)
			return
		}

		w.WriteHeader(http.StatusOK)

		h, ok := w.(http.Hijacker)
		if !ok {
			httpErr(http.StatusInternalServerError, fmt.Errorf("http.Hijacker: unavailable"))
			return
		}

		sconn, _, err := h.Hijack()
		if err != nil {
			httpErr(http.StatusServiceUnavailable, err)
			return
		}

		var src, dst = sconn.(*net.TCPConn), dconn.(*net.TCPConn)
		defer dst.CloseWrite()
		// TODO:
		var done = make(chan struct{})
		go func() {
			defer func() { done <- struct{}{} }()
			n, err := io.Copy(src, dst)
			log.Printf("src<-dst: n=%d error=%v", n, err)
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			n, err := io.Copy(dst, src)
			log.Printf("src->dst: n=%d error=%v", n, err)
		}()
		<-done
		<-done
	})
}

func newReverseProxy(target *url.URL) *httputil.ReverseProxy {
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
