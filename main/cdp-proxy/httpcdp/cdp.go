package httpcdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// devtools://devtools/bundled/inspector.html?experiments=true&v8only=true&ws=localhost:9229/cdp-proxy

// https://medium.com/@paul_irish/debugging-node-js-nightlies-with-chrome-devtools-7c4a1b95ae27
// conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"method": "Page.disable","params":{}}`)))
func handleCDPEvent(ctx context.Context, conn *websocket.Conn, e event) error {
	switch m := e.Method; {
	case m == "Page.canScreencast" ||
		m == "Network.canEmulateNetworkConditions" ||
		m == "Emulation.canEmulate":
		respond(conn, e.ID, `{"result":false}`)
	case m == "Page.getResourceTree":
		// Window decoration
		respond(conn, e.ID,
			`{"frameTree": { "frame":{"id":1,"url":"http://cdp-proxy","mimeType":"other"},"childFrames":[],"resources":[]}}`,
		)
	case m == "Network.getResponseBody":
		params, ok := e.Params.(map[string]interface{})
		if !ok {
			return nil
		}
		e.reqID, ok = params["requestId"].(string)
		if !ok {
			return nil
		}

		if buf, ok := store.Load(e.reqID); !ok {
			respond(conn, e.ID, `{"body":"","base64Encoded":true}`)
		} else {
			result := map[string]interface{}{
				"base64Encoded": true,
				"body":          base64.StdEncoding.Strict().EncodeToString(buf.Bytes()),
			}

			data, err := json.Marshal(result)
			if err != nil {
				log.Printf("json.Marshal: error=%q", err)
				return nil
			}

			// https://chromedevtools.github.io/devtools-protocol/1-2/Network#method-getResponseBody
			respond(conn, e.ID, string(data))
		}
	default:
		respond(conn, e.ID, `{}`)
	}

	return nil
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	// NOTE:
	// devtoolsFrontendUrl is not respected when opened from `chrome://inspect`
	var (
		hostPort = s.HostPort
		wsURL    = hostPort + "/cdp"
	)
	fmt.Fprintf(w, `[{
			"id": "cdp-proxy",
			"title": "cdp-proxy",
			"type": "proxy",
			"description": "cdp-proxy requests",
			"faviconUrl": "https://nodejs.org/static/favicon.ico",
			"url": %q,
			"devtoolsFrontendUrl": "ws=%s",
			"webSocketDebuggerUrl": "ws://%s"
		}]`, hostPort, wsURL, wsURL)
}

func writeConn(conn *websocket.Conn, p []byte) (int, error) {
	log.Printf("[CDP<-] %.120s", string(p))
	return len(p), conn.WriteMessage(websocket.TextMessage, p)
}

func respond(conn *websocket.Conn, id int, p string) (int, error) {
	return writeConn(conn, []byte(fmt.Sprintf(`{"id":%d,"result":%s}`, id, p)))
}
