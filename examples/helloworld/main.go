package main

import (
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
	HTTP_CDP_Addr    = "localhost:9229"
	HTTP_Server_Addr = "localhost:8081"
)

func main() {
	flag.StringVar(&HTTP_CDP_Addr, "http-cdp-addr", HTTP_CDP_Addr, "chrome devtools protocol listener address")
	flag.StringVar(&HTTP_Server_Addr, "http-proxy-addr", HTTP_Server_Addr, "HTTP server listener address")
	flag.Parse()

	var eb = httpcdp.NewEventBus()

	go func() {
		log.Printf("http.devtools: ListenAndServe.address=%q", HTTP_CDP_Addr)
		if err := http.ListenAndServe(HTTP_CDP_Addr, httpcdp.Devtools(eb)); err != nil {
			log.Fatalf("http.devtools: ListenAndServe.error=%q", err)
		}
	}()

	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "hello world")
		})

		log.Printf("http.proxy: ListenAndServe.address=%q", HTTP_Server_Addr)
		if err := http.ListenAndServe(HTTP_Server_Addr, cdphttp.Handler(eb, handler)); err != nil {
			log.Fatalf("http.proxy: ListenAndServe.error=%q", err)
		}
	}()

	sigc := make(chan os.Signal)
	signal.Notify(sigc, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(sigc)

	log.Printf("os: signal=%v", <-sigc)
}
