package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if client.name != "" {
		t.Fatalf("read-only endpoint retained username %q", client.name)
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

func TestOnlineUsersExcludeReadOnlyClients(t *testing.T) {
	visible := &Client{name: "member"}
	readOnly := &Client{name: "ignored", readOnly: true}
	server := &Server{clients: map[*Client]bool{
		visible:  true,
		readOnly: true,
	}}

	users := server.getOnlineUsersSnapshot()
	if len(users) != 1 || users[0] != "member" {
		t.Fatalf("online users are %q, want [member]", users)
	}
}

func TestPersistenceOrderFlushAndHistoryLimit(t *testing.T) {
	directory := t.TempDir()
	messagesPath := filepath.Join(directory, "messages.txt")
	checkPath := filepath.Join(directory, "password_check.txt")
	if err := os.WriteFile(checkPath, []byte("encrypted check token"), 0600); err != nil {
		t.Fatalf("write check token: %v", err)
	}

	server, err := NewServer(messagesPath, checkPath)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	server.Start()

	const messageCount = 100
	for i := 0; i < messageCount; i++ {
		server.incoming <- Message{
			Content:   fmt.Sprintf("message-%03d", i),
			Timestamp: time.Unix(int64(i), 0).UTC(),
			SenderID:  "test-sender",
		}
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}

	file, err := os.Open(messagesPath)
	if err != nil {
		t.Fatalf("open persisted messages: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	persisted := make([]Message, 0, messageCount)
	for scanner.Scan() {
		var message Message
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("decode persisted message %d: %v", len(persisted), err)
		}
		persisted = append(persisted, message)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan persisted messages: %v", err)
	}
	if len(persisted) != messageCount {
		t.Fatalf("persisted %d messages, want %d", len(persisted), messageCount)
	}
	for i, message := range persisted {
		want := fmt.Sprintf("message-%03d", i)
		if message.Content != want {
			t.Fatalf("persisted message %d is %q, want %q", i, message.Content, want)
		}
	}

	if len(server.messages) != historyLimit {
		t.Fatalf("runtime history contains %d messages, want %d", len(server.messages), historyLimit)
	}
	if server.msgCounter != messageCount {
		t.Fatalf("message counter is %d, want %d", server.msgCounter, messageCount)
	}
	for i, message := range server.messages {
		want := fmt.Sprintf("message-%03d", messageCount-historyLimit+i)
		if message.Content != want {
			t.Fatalf("runtime history message %d is %q, want %q", i, message.Content, want)
		}
	}

	restarted, err := NewServer(messagesPath, checkPath)
	if err != nil {
		t.Fatalf("restart server: %v", err)
	}
	if len(restarted.messages) != historyLimit {
		t.Fatalf("restarted history contains %d messages, want %d", len(restarted.messages), historyLimit)
	}
	if restarted.msgCounter != messageCount {
		t.Fatalf("restarted message counter is %d, want %d", restarted.msgCounter, messageCount)
	}
	for i, message := range restarted.messages {
		want := fmt.Sprintf("message-%03d", messageCount-historyLimit+i)
		if message.Content != want {
			t.Fatalf("restarted history message %d is %q, want %q", i, message.Content, want)
		}
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted server: %v", err)
	}
}
