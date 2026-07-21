package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/go-mysql-org/go-mysql/server"

	"github.com/dot5enko/cloudfunctions/packages/sqlite/sqlite"
	sqliteServer "github.com/dot5enko/cloudfunctions/packages/sqlite/server"
)

// Config holds the proxy configuration
type Config struct {
	// Server settings
	Address string
	Port    int

	// Database settings
	DatabasePath string
	WALMode      bool
	BusyTimeout  time.Duration
	MaxConns     int

	// Auth settings
	Username string
	Password string

	// Logging
	Debug bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Address:      "0.0.0.0",
		Port:         3306,
		DatabasePath: "./data.sqlite",
		WALMode:      true,
		BusyTimeout:  5 * time.Second,
		MaxConns:     10,
		Username:     "",
		Password:     "",
		Debug:        false,
	}
}

// Proxy is the main SQLite wire protocol proxy
type Proxy struct {
	config    Config
	pool      *sqlite.Pool
	handlers  *sqliteServer.HandlerFactory
	sessions  *sqliteServer.SessionManager
	listener  net.Listener
	ctx       context.Context
	cancel    context.CancelFunc
}

// New creates a new proxy instance
func New(config Config) (*Proxy, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create SQLite connection pool
	poolConfig := sqlite.PoolConfig{
		MaxOpenConns: config.MaxConns,
		MaxIdleConns: config.MaxConns / 2,
		MaxLifetime:  time.Hour,
		BusyTimeout:  config.BusyTimeout,
		WALMode:      config.WALMode,
	}

	pool, err := sqlite.NewPool(config.DatabasePath, poolConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}

	// Create session manager
	sessions := sqliteServer.NewSessionManager()

	handlers := sqliteServer.NewHandlerFactory(pool.DB())

	return &Proxy{
		config:   config,
		pool:     pool,
		handlers: handlers,
		sessions: sessions,
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// Start starts the proxy server
func (p *Proxy) Start() error {
	addr := fmt.Sprintf("%s:%d", p.config.Address, p.config.Port)

	// Create listener
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	p.listener = listener

	fmt.Printf("[INFO] SQLite Wire Proxy listening on %s\n", addr)
	fmt.Printf("[INFO] Database: %s\n", p.config.DatabasePath)
	fmt.Printf("[INFO] WAL mode: %v\n", p.config.WALMode)

	// Accept connections
	go p.acceptConnections()

	return nil
}

// acceptConnections handles incoming connections
func (p *Proxy) acceptConnections() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				fmt.Printf("[ERROR] Failed to accept connection: %v\n", err)
				continue
			}
		}

		go p.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
func (p *Proxy) handleConnection(conn net.Conn) {
	defer conn.Close()

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	session := p.sessions.Create(sessionID, p.config.Username)
	defer p.sessions.Remove(sessionID)

	handler := p.handlers.NewSessionHandler(session)

	mysqlConn, err := server.NewConn(
		conn,
		p.config.Username,
		p.config.Password,
		handler,
	)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create connection: %v\n", err)
		return
	}

	fmt.Printf("[INFO] Client connected: %s (session: %s)\n", conn.RemoteAddr(), sessionID)

	// Handle commands
	for {
		if err := mysqlConn.HandleCommand(); err != nil {
			fmt.Printf("[INFO] Client disconnected: %s (%v)\n", conn.RemoteAddr(), err)
			return
		}

		// Update session activity
		session.SetVariable("last_query", time.Now().Format(time.RFC3339))
	}
}

// Stop stops the proxy server
func (p *Proxy) Stop() error {
	fmt.Println("[INFO] Shutting down proxy...")

	// Cancel context to stop accepting connections
	p.cancel()

	// Close listener
	if p.listener != nil {
		p.listener.Close()
	}

	// Close database pool
	if p.pool != nil {
		p.pool.Close()
	}

	fmt.Println("[INFO] Proxy stopped")
	return nil
}

// Stats returns proxy statistics
func (p *Proxy) Stats() ProxyStats {
	return ProxyStats{
		ActiveSessions: p.sessions.Count(),
		DBStats:        p.pool.Stats(),
	}
}

// ProxyStats holds proxy statistics
type ProxyStats struct {
	ActiveSessions int
	DBStats        sql.DBStats
}
