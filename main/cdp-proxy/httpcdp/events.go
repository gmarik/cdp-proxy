package httpcdp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
)

type event struct {
	ID     int         `json:"id,omitempty"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
	// internal use
	reqID string `json:"-"`
}

type eventBus struct {
	ch chan event
}

func NewEventBus() *eventBus {
	return &eventBus{ch: make(chan event, 100)}
}

func (eb *eventBus) ReadEvent(ctx context.Context) event {
	select {
	case <-ctx.Done():
		return event{}
	case e := <-eb.ch:
		return e
	}
}

func (eb *eventBus) emit(e event) error {
	// TODO:
	// 1. skips events if no other goroutine is recv-ing concurrently
	// 2. it load-balances between multiple UIs which is not what expected
	select {
	case eb.ch <- e:
	default:
		log.Printf("SKIP: event.reqID=%s event.Method=%q\r\n", e.reqID, e.Method)
	}
	return nil
}

func (m *eventBus) RequestWillBeSent(req *http.Request) (reqID string) {
	vlog.Printf("RequestWillBeSent: %v", req)

	var t = time.Now()
	reqID = fmt.Sprintf("ID-%v", t.UnixNano())

	m.emit(event{
		Method: "Network.requestWillBeSent",
		Params: network.EventRequestWillBeSent{
			RequestID: network.RequestID(reqID),
			LoaderID:  "1",
			// TODO
			DocumentURL: req.URL.String(),
			// TODO:
			// FrameID:     "123.2",
			Request: &network.Request{
				InitialPriority: "High",
				ReferrerPolicy:  "no-referrer",
				Method:          req.Method,
				URL:             req.URL.String(),
				Headers:         headers(req.Header),
			},
			Timestamp: (*cdp.MonotonicTime)(&t),
			WallTime:  (*cdp.TimeSinceEpoch)(&t),
			Initiator: &network.Initiator{Type: "Other"},
			Type:      "Other",
		},
	})

	return reqID
}

func (m *eventBus) ResponseReceived(reqID string, re *http.Response) {
	vlog.Printf("ResponseReceived: reqID=%q response=%v", reqID, re)

	var t = time.Now()
	m.emit(event{
		Method: "Network.responseReceived",
		Params: network.EventResponseReceived{
			RequestID: network.RequestID(reqID),
			// TODO: map the document type
			Type:      network.ResourceTypeDocument,
			Timestamp: (*cdp.MonotonicTime)(&t),
			Response: &network.Response{
				FromDiskCache:     false,
				FromPrefetchCache: false,
				Headers:           headers(re.Header),
				RequestHeaders:    headers(re.Request.Header),
				EncodedDataLength: float64(re.ContentLength),
				MimeType:          "text/html", // re.Header.Get("Content-Type"),
				URL:               re.Request.URL.String(),
				Protocol:          re.Proto,
				StatusText:        re.Status,
				Status:            int64(re.StatusCode),
			},
		},
	})
}
func (m *eventBus) DataReceived(reqID string, data []byte) {
	vlog.Printf("DataReceived: reqID=%q data=%.10q", reqID, string(data))

	var t = time.Now()
	m.emit(event{
		Method: "_Data.chunk",
		Params: data,
		reqID:  reqID,
	})

	m.emit(event{
		Method: "Network.dataReceived",
		Params: network.EventDataReceived{
			RequestID:  network.RequestID(reqID),
			Timestamp:  (*cdp.MonotonicTime)(&t),
			DataLength: int64(len(data)),
			//TODO:
			// EncodedDataLength: int64(len(data)),
		},
	})
}
func (m *eventBus) LoadingFinished(reqID string, re *http.Response) {
	vlog.Printf("LoadingFinished: reqID=%q", reqID)

	var t = time.Now()
	m.emit(event{
		Method: "Network.loadingFinished",
		Params: network.EventLoadingFinished{
			RequestID:         network.RequestID(reqID),
			EncodedDataLength: float64(re.ContentLength),
			Timestamp:         (*cdp.MonotonicTime)(&t),
			// TODO:
			// ShouldReportCorbBlocking: false,
		},
	})
}
func (m *eventBus) LoadingFailed(reqID string, req *http.Request) {
	vlog.Printf("LoadingFailed: reqID=%q", reqID)
	var t = time.Now()
	m.emit(event{
		Method: "Network.loadingFailed",
		Params: network.EventLoadingFailed{
			RequestID: network.RequestID(reqID),
			ErrorText: "TODO",
			Type:      "Other",
			Canceled:  false,
			Timestamp: (*cdp.MonotonicTime)(&t),
		},
	})
}

func headers(h http.Header) network.Headers {
	var H = make(network.Headers)
	for k, _ := range h {
		// TODO: all values, not just the first one
		H[k] = h.Get(k)
	}
	return H
}
