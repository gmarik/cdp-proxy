package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	httpx "github.com/gmarik/cdp-proxy/http"
	"github.com/gmarik/cdp-proxy/main/cdp-proxy/httpcdp"
)

var (
	HTTP_CDP_HostPort   = "localhost:9229"
	HTTP_Proxy_HostPort = "localhost:8080"
)

func main() {
	flag.StringVar(&HTTP_CDP_HostPort, "http-cdp-addr", HTTP_CDP_HostPort, "Chrome Devtools Protocol(CDP) listener address(host:port)")
	flag.StringVar(&HTTP_Proxy_HostPort, "http-proxy-addr", HTTP_Proxy_HostPort, "HTTP proxy listener address(host:port)")
	flag.Parse()

	var (
		eb             = httpcdp.NewEventBus()
		ctx, cancel_Fn = context.WithCancel(context.Background())
	)
	defer cancel_Fn()

	go func() {
		var (
			px = "devtools: http.ListenAndServe:"
			s  = httpcdp.Server{
				Eventbus: eb,
				HostPort: HTTP_CDP_HostPort,
			}
		)
		defer log.Printf("%s done", px)
		log.Printf("%s address=%q", px, HTTP_CDP_HostPort)

		if err := s.ListenAndServe(ctx); err != nil {
			log.Fatalf("%s error=%q", px, err)
		}
	}()

	go func() {
		px := "proxy: http.ListenAndServe:"
		defer log.Printf("%s done", px)
		log.Printf("%s address=%q", px, HTTP_Proxy_HostPort)
		if err := http.ListenAndServe(HTTP_Proxy_HostPort, httpx.Handler(eb, proxy)); err != nil {
			log.Fatalf("%s error=%q", px, err)
		}
	}()

	sigc := make(chan os.Signal)
	signal.Notify(sigc, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(sigc)

	log.Printf("os: signal=%v", <-sigc)
}
