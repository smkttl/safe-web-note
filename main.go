package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	historyLimit     = 25
	messageQueueSize = 256
)

// Message represents a chat message
type Message struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	SenderID  string    `json:"senderId"` // IP or connection ID
}

// Client represents a WebSocket client
type Client struct {
	conn     *websocket.Conn
	send     chan []byte
	id       string
	name     string
	readOnly bool
	ctx      context.Context
	cancel   context.CancelFunc
}

// Server manages WebSocket clients and message persistence
type Server struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	incoming   chan Message
	persist    chan Message
	messages   []Message
	mu         sync.RWMutex
	file       *os.File
	msgCounter int
	checkToken string

	clientWG    sync.WaitGroup
	handlerWG   sync.WaitGroup
	startOnce   sync.Once
	closeOnce   sync.Once
	runDone     chan struct{}
	persistDone chan struct{}
	lifecycleMu sync.Mutex
	started     bool
	closing     bool
	closeErr    error
	persistErr  error
	persistMu   sync.Mutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for demo
	},
}

type SystemEvent struct {
	Type        string    `json:"type"`
	Text        string    `json:"text"`
	Timestamp   time.Time `json:"timestamp"`
	OnlineUsers []string  `json:"onlineUsers"`
}

func (s *Server) getOnlineUsersSnapshot() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]string, 0, len(s.clients))
	for client := range s.clients {
		if client.readOnly || client.name == "" {
			continue
		}
		users = append(users, client.name)
	}
	sort.Strings(users)
	return users
}

func normalizeIP(raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || strings.EqualFold(candidate, "unknown") {
		return ""
	}

	if strings.HasPrefix(candidate, "[") && strings.HasSuffix(candidate, "]") {
		candidate = candidate[1 : len(candidate)-1]
	}

	if ip := net.ParseIP(candidate); ip != nil {
		return ip.String()
	}

	host, _, err := net.SplitHostPort(candidate)
	if err == nil {
		host = strings.TrimPrefix(host, "[")
		host = strings.TrimSuffix(host, "]")
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}

	return ""
}

func getClientIP(r *http.Request, remoteAddr string) string {
	headerNames := []string{
		"CF-Connecting-IP",
		"X-Real-IP",
		"True-Client-IP",
		"X-Forwarded-For",
	}

	for _, headerName := range headerNames {
		raw := r.Header.Get(headerName)
		if raw == "" {
			continue
		}

		for _, part := range strings.Split(raw, ",") {
			if ip := normalizeIP(part); ip != "" {
				return ip
			}
		}
	}

	if ip := normalizeIP(remoteAddr); ip != "" {
		return ip
	}

	return remoteAddr
}

func sanitizeUsername(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}

	var b strings.Builder
	runeCount := 0
	for _, r := range name {
		if runeCount >= 32 {
			break
		}
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
		runeCount++
	}

	return strings.TrimSpace(b.String())
}

func (s *Server) broadcastSystemMessage(text string) {
	event := SystemEvent{
		Type:        "system",
		Text:        text,
		Timestamp:   time.Now(),
		OnlineUsers: s.getOnlineUsersSnapshot(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Error marshaling system event: %v", err)
		return
	}
	s.broadcastToClients(data)
}

// NewServer creates a new server instance
func NewServer(filename string, checkFile string) (*Server, error) {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	checkToken, err := os.ReadFile(checkFile)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to read check file: %v", err)
	}

	server := &Server{
		clients:     make(map[*Client]bool),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		incoming:    make(chan Message, messageQueueSize),
		persist:     make(chan Message, messageQueueSize),
		messages:    make([]Message, 0),
		file:        file,
		checkToken:  string(checkToken),
		runDone:     make(chan struct{}),
		persistDone: make(chan struct{}),
	}

	// Load existing messages from file
	if err := server.loadMessagesFromFile(); err != nil {
		log.Printf("Warning: failed to load messages from file: %v", err)
	}

	return server, nil
}

// loadMessagesFromFile loads existing messages from the file
func (s *Server) loadMessagesFromFile() error {
	// Seek to beginning of file
	if _, err := s.file.Seek(0, 0); err != nil {
		return err
	}

	scanner := bufio.NewScanner(s.file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]Message, 0, historyLimit)
	s.msgCounter = 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			log.Printf("Warning: failed to parse message line: %v", err)
			continue
		}
		if len(s.messages) == historyLimit {
			copy(s.messages, s.messages[1:])
			s.messages[len(s.messages)-1] = msg
		} else {
			s.messages = append(s.messages, msg)
		}
		s.msgCounter++
	}

	return scanner.Err()
}

func (s *Server) recordPersistenceError(err error) {
	if err == nil {
		return
	}
	log.Printf("Persistence error: %v", err)
	s.persistMu.Lock()
	if s.persistErr == nil {
		s.persistErr = err
	}
	s.persistMu.Unlock()
}

func (s *Server) persistenceLoop() {
	defer close(s.persistDone)
	for msg := range s.persist {
		data, err := json.Marshal(msg)
		if err != nil {
			s.recordPersistenceError(fmt.Errorf("marshal message: %w", err))
			continue
		}
		if _, err := s.file.Write(append(data, '\n')); err != nil {
			s.recordPersistenceError(fmt.Errorf("write message: %w", err))
			continue
		}
		if err := s.file.Sync(); err != nil {
			s.recordPersistenceError(fmt.Errorf("sync message file: %w", err))
		}
	}
}

func (s *Server) Start() {
	s.startOnce.Do(func() {
		s.lifecycleMu.Lock()
		defer s.lifecycleMu.Unlock()
		if s.closing {
			return
		}
		s.started = true
		go s.persistenceLoop()
		go s.Run()
	})
}

// Run starts the server's main loop
func (s *Server) Run() {
	defer close(s.runDone)
	for {
		select {
		case client := <-s.register:
			s.mu.Lock()
			s.clients[client] = true
			s.mu.Unlock()

			if client.readOnly {
				log.Printf("Read-only client %s connected", client.id)
			} else {
				log.Printf("Client %s (%s) connected", client.id, client.name)
			}

			// Send historical chat messages only.
			s.sendHistoricalMessages(client)
			if !client.readOnly {
				// Presence events are ephemeral: broadcast to online clients only.
				s.broadcastSystemMessage(fmt.Sprintf("%s joined", client.name))
			}

		case client := <-s.unregister:
			shouldBroadcastLeave := false
			s.mu.Lock()
			if _, ok := s.clients[client]; ok {
				delete(s.clients, client)
				close(client.send)
				client.cancel()
				if client.readOnly {
					log.Printf("Read-only client %s disconnected", client.id)
				} else {
					log.Printf("Client %s (%s) disconnected", client.id, client.name)
					shouldBroadcastLeave = true
				}
			}
			s.mu.Unlock()
			if shouldBroadcastLeave {
				// Presence event is ephemeral: not persisted/replayed.
				s.broadcastSystemMessage(fmt.Sprintf("%s left", client.name))
			}

		case message, ok := <-s.incoming:
			if !ok {
				return
			}
			s.acceptMessage(message)
		}
	}
}

func (s *Server) broadcastToClients(message []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.clients {
		select {
		case client.send <- message:
		default:
			close(client.send)
			delete(s.clients, client)
			client.cancel()
			client.conn.Close()
		}
	}
}

func (s *Server) acceptMessage(msg Message) {
	s.mu.Lock()
	if len(s.messages) == historyLimit {
		copy(s.messages, s.messages[1:])
		s.messages[len(s.messages)-1] = msg
	} else {
		s.messages = append(s.messages, msg)
	}
	s.msgCounter++
	s.mu.Unlock()

	// Queue persistence before broadcast so shutdown can flush every accepted message.
	s.persist <- msg

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}
	s.broadcastToClients(data)
}

// sendHistoricalMessages sends stored messages to a new client based on ignore parameter
func (s *Server) sendHistoricalMessages(client *Client) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.messages) == 0 {
		return
	}

	startIndex := len(s.messages) - 25
	if startIndex < 0 {
		startIndex = 0
	}

	// Send last 25 messages (or fewer)
	for i := startIndex; i < len(s.messages); i++ {
		msg := s.messages[i]
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("Error marshaling historical message: %v", err)
			continue
		}

		select {
		case client.send <- data:
		default:
			log.Printf("Client %s send buffer full", client.id)
			return
		}
	}
}

// handleWebSocket handles read-write WebSocket connections.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.handleWebSocketMode(w, r, false)
}

// handleReadOnlyWebSocket handles WebSocket connections that cannot publish.
func (s *Server) handleReadOnlyWebSocket(w http.ResponseWriter, r *http.Request) {
	s.handleWebSocketMode(w, r, true)
}

func (s *Server) beginWebSocketHandler() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing {
		return false
	}
	s.handlerWG.Add(1)
	return true
}

func (s *Server) handleWebSocketMode(w http.ResponseWriter, r *http.Request, readOnly bool) {
	if !s.beginWebSocketHandler() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	defer s.handlerWG.Done()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	// Create client with context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	clientID := getClientIP(r, conn.RemoteAddr().String())
	name := ""
	if !readOnly {
		name = sanitizeUsername(r.URL.Query().Get("username"))
		if name == "" {
			name = clientID
		}
	}
	client := &Client{
		conn:     conn,
		send:     make(chan []byte, 256),
		id:       clientID,
		name:     name,
		readOnly: readOnly,
		ctx:      ctx,
		cancel:   cancel,
	}

	// Register client
	s.register <- client

	// Start goroutines for reading and writing
	s.clientWG.Add(2)
	go func() {
		defer s.clientWG.Done()
		s.readPump(client)
	}()
	go func() {
		defer s.clientWG.Done()
		s.writePump(client)
	}()
}

// readPump handles incoming messages from client
func (s *Server) readPump(client *Client) {
	defer func() {
		s.unregister <- client
		client.conn.Close()
	}()

	client.conn.SetReadLimit(4 * 1024 * 1024)

	for {
		select {
		case <-client.ctx.Done():
			return
		default:
			_, message, err := client.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket read error for client %s: %v", client.id, err)
				}
				return
			}
			if client.readOnly {
				deadline := time.Now().Add(time.Second)
				client.conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(
						websocket.ClosePolicyViolation, "read-only connection"),
					deadline,
				)
				return
			}

			// Submit the message to the server loop for ordered acceptance.
			msg := Message{
				Content:   string(message),
				Timestamp: time.Now(),
				SenderID:  client.id,
			}
			select {
			case s.incoming <- msg:
			case <-client.ctx.Done():
				return
			}
		}
	}
}

// writePump handles outgoing messages to client
func (s *Server) writePump(client *Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case <-client.ctx.Done():
			return
		case message, ok := <-client.send:
			if !ok {
				// Channel closed
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("Write error for client %s: %v", client.id, err)
				return
			}

		case <-ticker.C:
			// Send ping to keep connection alive
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Close properly shuts down the server
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.closing = true
		started := s.started
		s.lifecycleMu.Unlock()

		if started {
			s.handlerWG.Wait()

			s.mu.RLock()
			clients := make([]*Client, 0, len(s.clients))
			for client := range s.clients {
				clients = append(clients, client)
			}
			s.mu.RUnlock()
			for _, client := range clients {
				client.cancel()
				client.conn.Close()
			}

			s.clientWG.Wait()
			close(s.incoming)
			<-s.runDone
			close(s.persist)
			<-s.persistDone
		}

		s.persistMu.Lock()
		persistenceErr := s.persistErr
		s.persistMu.Unlock()
		if err := s.file.Sync(); err != nil {
			persistenceErr = errors.Join(persistenceErr, fmt.Errorf("sync message file: %w", err))
		}
		if err := s.file.Close(); err != nil {
			persistenceErr = errors.Join(persistenceErr, fmt.Errorf("close message file: %w", err))
		}
		s.closeErr = persistenceErr
	})
	return s.closeErr
}

func main() {
	// Create server with message file
	server, err := NewServer("messages.txt", "password_check.txt")
	if err != nil {
		log.Fatal("Failed to create server:", err)
	}
	server.Start()

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "index.html")
	})
	mux.HandleFunc("/ws", server.handleWebSocket)
	mux.HandleFunc("/ws-readonly", server.handleReadOnlyWebSocket)

	// Add a simple status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		server.mu.RLock()
		defer server.mu.RUnlock()

		status := fmt.Sprintf("Server running\nConnected clients: %d\nTotal messages: %d\n",
			len(server.clients), server.msgCounter)
		w.Write([]byte(status))
	})

	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(server.checkToken))
	})

	// Start HTTP server
	addr := ":8080"
	log.Printf("Server starting on %s", addr)
	log.Printf("WebSocket endpoint: ws://%s/ws", addr)
	log.Printf("Read-only WebSocket endpoint: ws://%s/ws-readonly", addr)
	log.Printf("Status endpoint: http://%s/status", addr)

	httpServer := &http.Server{Addr: addr, Handler: mux}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()

	signalContext, stopSignals := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	var runErr error
	select {
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		runErr = httpServer.Shutdown(shutdownContext)
		cancel()
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	closeErr := server.Close()
	if runErr != nil {
		log.Printf("HTTP server error: %v", runErr)
	}
	if closeErr != nil {
		log.Printf("Server shutdown error: %v", closeErr)
	}
	if runErr != nil || closeErr != nil {
		os.Exit(1)
	}
}
