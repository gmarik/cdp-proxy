package httpcdp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

var vlog = log.New(ioutil.Discard, "", log.Lshortfile)

var wsUpgrader = &websocket.Upgrader{
	ReadBufferSize:  10 * 1024 * 1024,
	WriteBufferSize: 25 * 1024 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type eventReader interface {
	ReadEvent(context.Context) event
}

type entityStore interface {
	Get(reqID string) io.Writer
}

func Devtools(er eventReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u = r.URL

		// `/json` is too noisy
		if false == strings.HasPrefix(u.Path, "/json") {
			log.Printf("HTTP: %s\n", u.String())
			defer log.Printf("HTTP: %s: done\n", u.String())
		}

		switch {
		case u.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `go to <b>chrome-devtools://devtools/bundled/inspector.html?experiments=true&ws=localhost:9229/cdp-proxy</b>`)
		case u.Path == "/json":
			// https://chromedevtools.github.io/devtools-protocol/#get-json-or-jsonlist
			metadata(w, r)
		case u.Path == "/cdp-proxy":
			conn, err := wsUpgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Printf("HTTP: ws.Upgrader.Upgrade: error=%q", err)
				code := http.StatusServiceUnavailable
				http.Error(w, http.StatusText(code), code)
				return
			}

			if err := handleConn(er, conn); err != nil {
				log.Printf("handleCOnn: error=%q\n", err)
			}
		}
	})
}

func metadata(w http.ResponseWriter, r *http.Request) {
	// chrome-devtools://devtools/bundled/inspector.html?experiments=true&v8only=true&ws=localhost:9229/devtools/cdp-proxy

	// NOTE:
	// devtoolsFrontendUrl is not respected when opened from `chrome://inspect`
	host := "localhost:9229"
	wsURL := host + "/cdp-proxy"
	fmt.Fprintf(w, `[{
			"id": "cdp-proxy",
			"type": "proxy",
			"title": "cdp-proxy",
			"description": "cdp-proxy requests",
			"faviconUrl": "https://nodejs.org/static/favicon.ico",
			"url": "localhost:8080",
			"devtoolsFrontendUrl": "ws=%s",
			"webSocketDebuggerUrl": "ws://%s"
		}]`, wsURL, wsURL)
}

func handleConn(er eventReader, conn *websocket.Conn) error {
	ctx, cancel_Fn := context.WithCancel(context.Background())
	defer conn.Close()
	defer cancel_Fn()

	const buffer_Size = 100 // totally random
	httpEventsc := make(chan event, buffer_Size)
	cdpEventsc := make(chan event, buffer_Size)

	bodystore := make(map[string]*bytes.Buffer)

	go func() {
		defer cancel_Fn()
		for {
			var e = er.ReadEvent(ctx)
			select {
			case <-ctx.Done():
				return
			// read events into buffered channel so no events are lost
			case httpEventsc <- e:
			}
		}
	}()

	writeConn := func(p []byte) (int, error) {
		log.Printf("[CDP<-] %.120s", string(p))
		return len(p), conn.WriteMessage(websocket.TextMessage, p)
	}

	respond := func(id int, p string) (int, error) {
		return writeConn([]byte(fmt.Sprintf(`{"id":%d,"result":%s}`, id, p)))
	}

	go func() {
		defer cancel_Fn()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var msg event
				if err := websocket.ReadJSON(conn, &msg); err != nil {
					log.Printf("ws.ReadJSON: error=%q", err)
					return
				}
				cdpEventsc <- msg
			}
		}
	}()

	// https://medium.com/@paul_irish/debugging-node-js-nightlies-with-chrome-devtools-7c4a1b95ae27
	// conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"method": "Page.disable","params":{}}`)))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-httpEventsc:
			log.Printf("[HTTP->] %s", e.Method)
			if e.Method == "_Data.chunk" {
				data, ok := e.Params.([]byte)
				if !ok {
					continue
				}
				buf, ok := bodystore[e.reqID]
				if !ok {
					buf = new(bytes.Buffer)
					bodystore[e.reqID] = buf
				}
				if _, err := buf.Write(data); err != nil {
					log.Printf("buf.Write: error=%q", err)
				}
				continue
			}

			if err := websocket.WriteJSON(conn, e); err != nil {
				log.Printf("writeEvent: error=%q", err)
				return err
			}
		case msg := <-cdpEventsc:
			log.Printf("[CDP->] %+v", msg)
			switch m := msg.Method; {
			case m == "Page.canScreencast" ||
				m == "Network.canEmulateNetworkConditions" ||
				m == "Emulation.canEmulate":

				respond(msg.ID, `{"result":false}`)
			case m == "Page.getResourceTree":
				// Window decoration
				payload := `{"frameTree": { "frame":{"id":1,"url":"http://cdp-proxy","mimeType":"other"},"childFrames":[],"resources":[]}}`
				respond(msg.ID, payload)
			case m == "Network.getResponseBody":
				params, ok := msg.Params.(map[string]interface{})
				if !ok {
					continue
				}
				msg.reqID, ok = params["requestId"].(string)
				if !ok {
					continue
				}
				buf, ok := bodystore[msg.reqID]
				if !ok {
					respond(msg.ID, `{"body":"","base64Encoded":true}`)
				} else {
					result := map[string]interface{}{
						"base64Encoded": true,
						"body":          base64.StdEncoding.Strict().EncodeToString(buf.Bytes()),
					}

					data, err := json.Marshal(result)
					if err != nil {
						log.Printf("json.Marshal: error=%q", err)
						continue
					}

					// https://chromedevtools.github.io/devtools-protocol/1-2/Network#method-getResponseBody
					respond(msg.ID, string(data))
				}
			default:
				respond(msg.ID, `{}`)
			}
		}
	}
}
