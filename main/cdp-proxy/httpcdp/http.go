package httpcdp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/gorilla/websocket"
)

var requestLog = newStore()

var vlog = log.New(ioutil.Discard, "", log.Lshortfile)

var wsUpgrader = &websocket.Upgrader{
	ReadBufferSize:  10 * 1024 * 1024,
	WriteBufferSize: 25 * 1024 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type eventReader interface {
	ReadEvent(context.Context, *event) error
	Close()
}

type entityStore interface {
	Get(reqID string) io.Writer
}

type Server struct {
	// Debug is a CSV of http prefixes to log requests of. Default: ""
	Verbose     string
	Eventbus    *eventBus
	HostPort    string
	verboseList []string
}

func (h *Server) init() {
	if h.Verbose != "" {
		h.verboseList = strings.Split(h.Verbose, ",")
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	s.init()

	errc := make(chan error)
	go func() {
		if err := http.ListenAndServe(s.HostPort, http.HandlerFunc(s.serveHTTP)); err != nil {
			errc <- fmt.Errorf("http.ListenAndServe: %w", err)
		}
	}()

	return <-errc
}

func (h *Server) isVerbose(path string) bool {
	for i := range h.verboseList {
		if strings.HasPrefix(path, h.verboseList[i]) {
			log.Printf("OK %q: %q, %#v\n", path, h.verboseList[i], h.verboseList)
			return true
		}
	}
	return false
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p := r.URL, r.URL.Path; len(p) > 1 && p[0] == '/' && s.isVerbose(p[1:]) {
		log.Printf("HTTP: start: %s\n", u.String())
		defer log.Printf("HTTP: done: %s\n", u.String())
	}

	switch u := r.URL; {
	case u.Path == "/json":
		// https://chromedevtools.github.io/devtools-protocol/#get-json-or-jsonlist
		s.metadata(w, r)
	case u.Path == "/" && hasHeader(r, "Connection", "Upgrade") && hasHeader(r, "Upgrade", "Websocket"):
		// The endpoint, DevTools connects to to listen for the CDP events
		// It's a bidirectional Websocket connection,
		// which translates events from `eventReader` to CDP protocol

		/*
		 GET / HTTP/1.1
		 Host: localhost:9229
		 Accept-Encoding: gzip, deflate, br
		 Accept-Language: en-US,en;q=0.9
		 Cache-Control: no-cache
		 Connection: Upgrade
		 Cookie: _ga=GA1.1.193655733.1558655142
		 Origin: chrome://devtools
		 Pragma: no-cache
		 Sec-Websocket-Extensions: permessage-deflate; client_max_window_bits
		 Sec-Websocket-Key: aaaaaB1q9FnARRKcArMLYQ==
		 Sec-Websocket-Version: 13
		 Upgrade: websocket
		 User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/83.0.4103.61 Safari/537.36
		*/
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			errr := fmt.Errorf("HTTP: ws.Upgrader: Upgrade: error=%q", err)
			http.Error(w, errr.Error(), http.StatusServiceUnavailable)
			return
		}
		ctx, cancel_Fn := context.WithCancel(r.Context())
		defer conn.Close()
		defer cancel_Fn()
		log.Printf("client: connected\n")
		defer log.Printf("client: disconnected\n")

		if err := s.handleConn(ctx, conn); err != nil {
			log.Printf("handleConn: error=%q\n", err)
		}
	case u.Path == "/":
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<br/> 1. copy below link and 
			<br/> 2. paste it into chrome's addressbar input
			<br/> 3. observe the DevTools UI
			<br/> 4. it's not a "normal" hyperlink so can't be clicked on
			<br/><br/>
			<b> chrome://devtools/bundled/inspector.html?ws=%s</b>`,
			s.HostPort,
		)
	}
}
func hasHeader(r *http.Request, key, value string) bool {
	for _, v := range r.Header[textproto.CanonicalMIMEHeaderKey(key)] {
		if strings.EqualFold(v, value) {
			return true
		}
	}

	return false
}

func (s *Server) handleConn(ctx context.Context, conn *websocket.Conn) error {
	// NOTE: make sure to not block the goroutines to be able to errc <- err
	var errc = make(chan error, 2)
	reader := func() {
		er := s.Eventbus.NewReader()
		defer er.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var e event
				if err := er.ReadEvent(ctx, &e); err != nil {
					errc <- fmt.Errorf("eventbus.ReadEvent: %w", err)
					return
				}
				// events coming from mitm proxy
				log.Printf("[MITM->] %s", e.Method)
				if e.Method == "_Data.chunk" {
					data, ok := e.Params.([]byte)
					if !ok {
						continue
					}

					var newBuf bytes.Buffer
					buf, ok := requestLog.LoadOrStore(e.reqID, &newBuf)

					if _, err := buf.Write(data); err != nil {
						log.Printf("buf.Write: error=%q", err)
					}
					continue
				}

				if err := websocket.WriteJSON(conn, e); err != nil {
					errc <- fmt.Errorf("websocket.WriteJSON: %w", err)
					return
				}
			}
		}
	}

	writer := func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var e event
				if err := websocket.ReadJSON(conn, &e); err != nil {
					errc <- fmt.Errorf("websocket.ReadJSON: %w", err)
					return
				}
				log.Printf("[CDP->] %+v\n", e)
				if err := handleCDPEvent(ctx, conn, e); err != nil {
					errc <- fmt.Errorf("handleCDPEvent: %w", err)
					return
				}
			}
		}
	}

	go reader()
	go writer()

	for {
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
