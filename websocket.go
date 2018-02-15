package websocket

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"crypto/tls"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
	"github.com/sirupsen/logrus"
)

const (
	NotConnected = "websocket not connected"
)

var ErrNotConnected = errors.New(NotConnected)

type Websocket struct {
	// default to 2 seconds
	ReconnectIntervalMin time.Duration
	// default to 30 seconds
	ReconnectIntervalMax time.Duration
	// interval, default to 1.5
	ReconnectIntervalFactor float64
	// default to 2 seconds
	HandshakeTimeout time.Duration
	// Verbose suppress connecting/reconnecting messages.
	Verbose bool

	// Cal function
	OnConnect func(ws *Websocket)
	OnError   func(ws *Websocket, err error)

	logger *logrus.Logger

	dialer        *websocket.Dialer
	url           string
	requestHeader http.Header
	httpResponse  *http.Response
	mu            sync.Mutex
	dialErr       error
	isConnected   bool

	*websocket.Conn
}

func (ws *Websocket) WriteJSON(v interface{}) error {
	err := ErrNotConnected
	if ws.IsConnected() {
		err = ws.Conn.WriteJSON(v)
		if err != nil {
			ws.OnError(ws, err)
			ws.closeAndReconnect()
		}
	}

	return err
}

func (ws *Websocket) WriteMessage(messageType int, data []byte) error {
	err := ErrNotConnected
	if ws.IsConnected() {
		err = ws.Conn.WriteMessage(messageType, data)
		if err != nil {
			ws.OnError(ws, err)
			ws.closeAndReconnect()
		}
	}

	return err
}

func (ws *Websocket) ReadMessage() (messageType int, message []byte, err error) {
	err = ErrNotConnected
	if ws.IsConnected() {
		messageType, message, err = ws.Conn.ReadMessage()
		if err != nil {
			ws.OnError(ws, err)
			ws.closeAndReconnect()
		}
	}

	return
}

func (ws *Websocket) Close() {
	ws.mu.Lock()
	if ws.Conn != nil {
		ws.Conn.Close()
	}
	ws.isConnected = false
	ws.mu.Unlock()
}

func (ws *Websocket) closeAndReconnect() {
	ws.Close()
	ws.connect()
}

func (ws *Websocket) Dial(urlStr string, reqHeader http.Header) {
	if ws.logger == nil {
		ws.logger = logrus.New()
	}
	if urlStr == "" {
		ws.logger.Fatal("Dial: Url cannot be empty")
	}
	u, err := url.Parse(urlStr)

	if err != nil {
		ws.logger.Fatal("Url:", err)
	}

	if u.Scheme != "ws" && u.Scheme != "wss" {
		ws.logger.Fatal("Url: websocket URIs must start with ws or wss scheme")
	}

	if u.User != nil {
		ws.logger.Fatal("Url: user name and password are not allowed in websocket URIs")
	}

	ws.url = urlStr

	ws.setDefaults()

	// todo: add option function to configure dialer
	ws.dialer = &websocket.Dialer{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	ws.dialer.HandshakeTimeout = ws.HandshakeTimeout

	go ws.connect()

	// wait on first attempt
	time.Sleep(ws.HandshakeTimeout)
}

func (ws *Websocket) connect() {
	b := &backoff.Backoff{
		Min:    ws.ReconnectIntervalMin,
		Max:    ws.ReconnectIntervalMax,
		Factor: ws.ReconnectIntervalFactor,
		Jitter: true,
	}

	// seed rand for backoff
	rand.Seed(time.Now().UTC().UnixNano())

	for {
		nextInterval := b.Duration()

		wsConn, httpResp, err := ws.dialer.Dial(ws.url, ws.requestHeader)

		ws.mu.Lock()
		ws.Conn = wsConn
		ws.dialErr = err
		ws.isConnected = err == nil
		ws.httpResponse = httpResp
		ws.mu.Unlock()

		if err == nil {
			if ws.Verbose {
				ws.logger.Info(fmt.Sprintf("Dial: connection was successfully established with %s\n", ws.url))
			}
			break
		} else {
			if ws.Verbose {
				ws.logger.Error(err)
				ws.logger.Info(fmt.Sprintf("Dial: can't connect to %s, will try again in %d\n", ws.url, nextInterval))
			}
		}

		time.Sleep(nextInterval)
	}

	if ws.OnConnect != nil {
		ws.OnConnect(ws)
	}
}

func (ws *Websocket) GetHTTPResponse() *http.Response {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.httpResponse
}

func (ws *Websocket) GetDialError() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.dialErr
}

func (ws *Websocket) IsConnected() bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.isConnected
}

func (ws *Websocket) setDefaults() {
	if ws.ReconnectIntervalMin == 0 {
		ws.ReconnectIntervalMin = 2 * time.Second
	}

	if ws.ReconnectIntervalMax == 0 {
		ws.ReconnectIntervalMax = 30 * time.Second
	}

	if ws.ReconnectIntervalFactor == 0 {
		ws.ReconnectIntervalFactor = 1.5
	}

	if ws.HandshakeTimeout == 0 {
		ws.HandshakeTimeout = 2 * time.Second
	}
}
