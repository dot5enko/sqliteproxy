package sqlite

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// PoolConfig holds configuration for the SQLite connection pool
type PoolConfig struct {
	MaxOpenConns int
	MaxIdleConns int
	MaxLifetime  time.Duration
	BusyTimeout  time.Duration
	WALMode      bool
}

// DefaultPoolConfig returns sensible defaults
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns: 10,
		MaxIdleConns: 5,
		MaxLifetime:  time.Hour,
		BusyTimeout:  5 * time.Second,
		WALMode:      true,
	}
}

// Pool manages SQLite database connections
type Pool struct {
	db     *sql.DB
	config PoolConfig
	mu     sync.RWMutex
}

// NewPool creates a new SQLite connection pool
func NewPool(dbPath string, config PoolConfig) (*Pool, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=%s&_busy_timeout=%d&_foreign_keys=on",
		dbPath,
		walMode(config.WALMode),
		int(config.BusyTimeout.Milliseconds()),
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.MaxLifetime)

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable WAL mode if configured
	if config.WALMode {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
		}
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	return &Pool{
		db:     db,
		config: config,
	}, nil
}

// DB returns the underlying sql.DB
func (p *Pool) DB() *sql.DB {
	return p.db
}

// Close closes the database connection pool
func (p *Pool) Close() error {
	return p.db.Close()
}

// Stats returns pool statistics
func (p *Pool) Stats() sql.DBStats {
	return p.db.Stats()
}

func walMode(enabled bool) string {
	if enabled {
		return "WAL"
	}
	return "DELETE"
}
