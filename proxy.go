package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/server"

	"github.com/dot5enko/cloudfunctions/packages/sqlite/management"
	sqliteServer "github.com/dot5enko/cloudfunctions/packages/sqlite/server"
	"github.com/dot5enko/cloudfunctions/packages/sqlite/storage"
)

// Config holds the proxy configuration
type Config struct {
	// MySQL wire protocol
	Address string
	Port    int

	// Storage / tenant databases
	StorageRoot string
	WALMode     bool
	BusyTimeout time.Duration
	MaxConns    int

	// HTTP management API
	ManagementAddress string
	ManagementPort    int

	// Logging
	Debug bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Address:           "0.0.0.0",
		Port:              3306,
		StorageRoot:       "./storage",
		WALMode:           true,
		BusyTimeout:       5 * time.Second,
		MaxConns:          10,
		ManagementAddress: "127.0.0.1",
		ManagementPort:    8080,
		Debug:             false,
	}
}

// Proxy is the main SQLite wire protocol proxy
type Proxy struct {
	config     Config
	store      *storage.Store
	handlers   *sqliteServer.HandlerFactory
	sessions   *sqliteServer.SessionManager
	mysqlSrv   *server.Server
	listener   net.Listener
	httpServer *http.Server
	httpLn     net.Listener

	ctx    context.Context
	cancel context.CancelFunc

	wg       sync.WaitGroup
	connMu   sync.Mutex
	conns    map[net.Conn]struct{}
	stopping bool
}

// New creates a new proxy instance
func New(config Config) (*Proxy, error) {
	if config.StorageRoot == "" {
		return nil, fmt.Errorf("storage root is required")
	}
	if config.MaxConns <= 0 {
		config.MaxConns = 10
	}
	if config.BusyTimeout <= 0 {
		config.BusyTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	store, err := storage.Open(storage.StoreConfig{
		Root:        config.StorageRoot,
		WALMode:     config.WALMode,
		BusyTimeout: config.BusyTimeout,
		MaxConns:    config.MaxConns,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open storage: %w", err)
	}

	return &Proxy{
		config:   config,
		store:    store,
		handlers: sqliteServer.NewHandlerFactory(),
		sessions: sqliteServer.NewSessionManager(),
		mysqlSrv: server.NewDefaultServer(),
		ctx:      ctx,
		cancel:   cancel,
		conns:    make(map[net.Conn]struct{}),
	}, nil
}

// Start starts the proxy server
func (p *Proxy) Start() error {
	mysqlAddr := fmt.Sprintf("%s:%d", p.config.Address, p.config.Port)
	mysqlLn, err := net.Listen("tcp", mysqlAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", mysqlAddr, err)
	}
	p.listener = mysqlLn

	mgmtAddr := fmt.Sprintf("%s:%d", p.config.ManagementAddress, p.config.ManagementPort)
	httpLn, err := net.Listen("tcp", mgmtAddr)
	if err != nil {
		mysqlLn.Close()
		return fmt.Errorf("failed to listen on management %s: %w", mgmtAddr, err)
	}
	p.httpLn = httpLn

	p.httpServer = &http.Server{
		Handler:           management.NewHandler(p.store),
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("[INFO] SQLite Wire Proxy listening on %s\n", mysqlLn.Addr().String())
	fmt.Printf("[INFO] Management API listening on http://%s\n", httpLn.Addr().String())
	fmt.Printf("[INFO] Storage root: %s\n", p.store.Root())
	fmt.Printf("[INFO] WAL mode: %v\n", p.config.WALMode)

	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		p.acceptConnections()
	}()
	go func() {
		defer p.wg.Done()
		if err := p.httpServer.Serve(httpLn); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[ERROR] Management API error: %v\n", err)
		}
	}()

	return nil
}

// MySQLAddr returns the bound MySQL listen address.
func (p *Proxy) MySQLAddr() string {
	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

// ManagementAddr returns the bound management listen address.
func (p *Proxy) ManagementAddr() string {
	if p.httpLn == nil {
		return ""
	}
	return p.httpLn.Addr().String()
}

// Store exposes the storage registry (primarily for tests).
func (p *Proxy) Store() *storage.Store {
	return p.store
}

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

		p.connMu.Lock()
		if p.stopping {
			p.connMu.Unlock()
			conn.Close()
			continue
		}
		p.conns[conn] = struct{}{}
		p.connMu.Unlock()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() {
				p.connMu.Lock()
				delete(p.conns, conn)
				p.connMu.Unlock()
			}()
			p.handleConnection(conn)
		}()
	}
}

func (p *Proxy) handleConnection(conn net.Conn) {
	defer conn.Close()

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	session := p.sessions.Create(sessionID)
	defer p.sessions.Remove(sessionID)

	binding := &sqliteServer.ConnectionBinding{}
	handler := p.handlers.NewSessionHandler(session, binding)
	provider := sqliteServer.NewCredentialProvider(p.store, binding)

	mysqlConn, err := server.NewCustomizedConn(conn, p.mysqlSrv, provider, handler)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create connection: %v\n", err)
		return
	}

	if err := handler.Finalize(mysqlConn.GetUser()); err != nil {
		fmt.Printf("[ERROR] Failed to finalize database binding: %v\n", err)
		return
	}

	fmt.Printf("[INFO] Client connected: %s (session: %s, user: %s, db: %s)\n",
		conn.RemoteAddr(), sessionID, mysqlConn.GetUser(), session.GetDatabase())

	for {
		if err := mysqlConn.HandleCommand(); err != nil {
			fmt.Printf("[INFO] Client disconnected: %s (%v)\n", conn.RemoteAddr(), err)
			return
		}
		session.SetVariable("last_query", time.Now().Format(time.RFC3339))
	}
}

// Stop stops the proxy server
func (p *Proxy) Stop() error {
	fmt.Println("[INFO] Shutting down proxy...")

	p.connMu.Lock()
	p.stopping = true
	p.connMu.Unlock()

	p.cancel()

	if p.listener != nil {
		p.listener.Close()
	}

	if p.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = p.httpServer.Shutdown(ctx)
		cancel()
	}

	p.connMu.Lock()
	for conn := range p.conns {
		conn.Close()
	}
	p.connMu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		fmt.Println("[WARN] Timed out waiting for connections to drain")
	}

	if p.store != nil {
		if err := p.store.Close(); err != nil {
			fmt.Printf("[ERROR] Failed to close storage: %v\n", err)
		}
	}

	fmt.Println("[INFO] Proxy stopped")
	return nil
}

// Stats returns proxy statistics
func (p *Proxy) Stats() ProxyStats {
	return ProxyStats{
		ActiveSessions: p.sessions.Count(),
		DBStats:        p.store.Stats(),
	}
}

// ProxyStats holds proxy statistics
type ProxyStats struct {
	ActiveSessions int
	DBStats        map[string]sql.DBStats
}
