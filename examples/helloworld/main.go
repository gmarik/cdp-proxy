package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	cdphttp "github.com/gmarik/cdp-proxy/http"
	httpcdp "github.com/gmarik/cdp-proxy/main/cdp-proxy/httpcdp"
)

var (
	HTTP_CDP_HostPort   = "localhost:9229"
	HTTP_Proxy_HostPort = "localhost:8081"
)

func main() {
	flag.StringVar(&HTTP_CDP_HostPort, "http-cdp-hostport", HTTP_CDP_HostPort, "chrome devtools protocol listener address(host:port)")
	flag.StringVar(&HTTP_Proxy_HostPort, "http-proxy-hostport", HTTP_Proxy_HostPort, "HTTP proxy listener address(host:port)")
	flag.Parse()

	var eb = httpcdp.NewEventBus()
	var ctx = context.Background()

	go func() {
		log.Printf("devtools: http.ListenAndServe: hostport=%q", HTTP_CDP_HostPort)
		s := httpcdp.Server{
			Eventbus: eb,
			HostPort: HTTP_CDP_HostPort,
		}
		if err := s.ListenAndServe(ctx); err != nil {
			log.Fatalf("devtools: http.ListenAndServe: error=%q", err)
		}
	}()

	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "hello world")
		})

		log.Printf("proxy: http.ListenAndServe: hostport=%q", HTTP_Proxy_HostPort)
		if err := http.ListenAndServe(HTTP_Proxy_HostPort, cdphttp.Handler(eb, handler)); err != nil {
			log.Fatalf("proxy: http.ListenAndServe: error=%q", err)
		}
	}()

	sigc := make(chan os.Signal)
	signal.Notify(sigc, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(sigc)

	log.Printf("os: signal=%v", <-sigc)
}
