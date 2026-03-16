package main

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "sync"
    "time"
    "github.com/gorilla/websocket"
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
    ctx      context.Context
    cancel   context.CancelFunc
}

// Server manages WebSocket clients and message persistence
type Server struct {
    clients    map[*Client]bool
    register   chan *Client
    unregister chan *Client
    broadcast  chan []byte
    messages   []Message
    mu         sync.RWMutex
    file       *os.File
    fileMutex  sync.Mutex
    msgCounter int
}

var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true // Allow all origins for demo
    },
}

// NewServer creates a new server instance
func NewServer(filename string) (*Server, error) {
    file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
    if err != nil {
        return nil, fmt.Errorf("failed to open file: %v", err)
    }

    server := &Server{
        clients:    make(map[*Client]bool),
        register:   make(chan *Client),
        unregister: make(chan *Client),
        broadcast:  make(chan []byte, 256),
        messages:   make([]Message, 0),
        file:       file,
    }

    // Load existing messages from file
    if err := server.loadMessagesFromFile(); err != nil {
        log.Printf("Warning: failed to load messages from file: %v", err)
    }

    return server, nil
}

// loadMessagesFromFile loads existing messages from the file
func (s *Server) loadMessagesFromFile() error {
    s.fileMutex.Lock()
    defer s.fileMutex.Unlock()

    // Seek to beginning of file
    if _, err := s.file.Seek(0, 0); err != nil {
        return err
    }

    scanner := bufio.NewScanner(s.file)
    s.mu.Lock()
    defer s.mu.Unlock()
    
    s.messages = make([]Message, 0)
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
        s.messages = append(s.messages, msg)
        s.msgCounter++
    }
    
    return scanner.Err()
}

// saveMessageToFile saves a message to the file asynchronously
func (s *Server) saveMessageToFile(msg Message) {
    go func() {
        s.fileMutex.Lock()
        defer s.fileMutex.Unlock()
        
        data, err := json.Marshal(msg)
        if err != nil {
            log.Printf("Error marshaling message: %v", err)
            return
        }
        
        if _, err := s.file.Write(append(data, '\n')); err != nil {
            log.Printf("Error writing to file: %v", err)
        }
        
        s.file.Sync() // Ensure data is written to disk
    }()
}

// Run starts the server's main loop
func (s *Server) Run() {
    for {
        select {
        case client := <-s.register:
            s.mu.Lock()
            s.clients[client] = true
            s.mu.Unlock()
            
            log.Printf("Client %s connected", client.id)
            
            // Send historical messages
            s.sendHistoricalMessages(client)
            
        case client := <-s.unregister:
            s.mu.Lock()
            if _, ok := s.clients[client]; ok {
                delete(s.clients, client)
                close(client.send)
                client.cancel()
                log.Printf("Client %s disconnected", client.id)
            }
            s.mu.Unlock()
            
        case message := <-s.broadcast:
            s.mu.RLock()
            for client := range s.clients {
                select {
                case client.send <- message:
                default:
                    close(client.send)
                    delete(s.clients, client)
                }
            }
            s.mu.RUnlock()
        }
    }
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

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("Failed to upgrade connection: %v", err)
        return
    }
    
    // Create client with context for cancellation
    ctx, cancel := context.WithCancel(context.Background())
    client := &Client{
        conn:   conn,
        send:   make(chan []byte, 256),
        id:     conn.RemoteAddr().String(),
        ctx:    ctx,
        cancel: cancel,
    }
    
    // Register client
    s.register <- client
    
    // Start goroutines for reading and writing
    go s.readPump(client)
    go s.writePump(client)
}

// readPump handles incoming messages from client
func (s *Server) readPump(client *Client) {
    defer func() {
        s.unregister <- client
        client.conn.Close()
    }()
    
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
            
            // Create message object
            msg := Message{
                Content:   string(message),
                Timestamp: time.Now(),
                SenderID:  client.id,
            }
            
            // Store in memory
            s.mu.Lock()
            s.messages = append(s.messages, msg)
            s.msgCounter++
            s.mu.Unlock()
            
            // Save to file asynchronously
            s.saveMessageToFile(msg)
            
            // Marshal for broadcasting
            data, err := json.Marshal(msg)
            if err != nil {
                log.Printf("Error marshaling message: %v", err)
                continue
            }
            
            // Broadcast to all clients
            s.broadcast <- data
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
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Close all client connections
    for client := range s.clients {
        client.cancel()
        client.conn.Close()
    }
    
    // Close file
    if err := s.file.Close(); err != nil {
        return fmt.Errorf("error closing file: %v", err)
    }
    
    return nil
}

func main() {
    // Create server with message file
    server, err := NewServer("messages.txt")
    if err != nil {
        log.Fatal("Failed to create server:", err)
    }
    defer server.Close()
    
    // Start server goroutine
    go server.Run()
    
    // Set up HTTP routes
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/" {
            http.NotFound(w, r)
            return
        }
        http.ServeFile(w, r, "index.html")
    })
    http.HandleFunc("/ws", server.handleWebSocket)
    
    // Add a simple status endpoint
    http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
        server.mu.RLock()
        defer server.mu.RUnlock()
        
        status := fmt.Sprintf("Server running\nConnected clients: %d\nTotal messages: %d\n",
            len(server.clients), server.msgCounter)
        w.Write([]byte(status))
    })
    
    // Start HTTP server
    addr := ":8080"
    log.Printf("Server starting on %s", addr)
    log.Printf("WebSocket endpoint: ws://%s/ws", addr)
    log.Printf("Status endpoint: http://%s/status", addr)
    
    if err := http.ListenAndServe(addr, nil); err != nil {
        log.Fatal("ListenAndServe:", err)
    }
}
