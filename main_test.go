package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadOnlyWebSocketReceivesButCannotPublish(t *testing.T) {
	server := &Server{
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
	registered := make(chan *Client, 1)
	disconnected := make(chan struct{})
	go func() {
		client := <-server.register
		registered <- client
		client = <-server.unregister
		client.cancel()
		close(disconnected)
	}()

	httpServer := httptest.NewServer(http.HandlerFunc(server.handleReadOnlyWebSocket))
	defer httpServer.Close()

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "?username=viewer"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial read-only endpoint: %v", err)
	}
	defer conn.Close()

	client := <-registered
	if !client.readOnly {
		t.Fatal("read-only endpoint registered a writable client")
	}

	client.send <- []byte("encrypted broadcast")
	_, received, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read broadcast: %v", err)
	}
	if string(received) != "encrypted broadcast" {
		t.Fatalf("received %q, want encrypted broadcast", received)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("must not publish")); err != nil {
		t.Fatalf("write test message: %v", err)
	}
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("read-only write returned %v, want close code %d", err, websocket.ClosePolicyViolation)
	}

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("read-only client was not unregistered")
	}
	if len(server.messages) != 0 || server.msgCounter != 0 {
		t.Fatal("read-only message entered server history")
	}
}
