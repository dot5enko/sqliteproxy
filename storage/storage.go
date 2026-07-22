package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	sqlitepkg "github.com/dot5enko/sqliteproxy/sqlite"
)

var (
	ErrNotFound     = errors.New("database not found")
	ErrInvalidLabel = errors.New("invalid database label")
)

const StatusReady = "ready"

type StoreConfig struct {
	Root        string
	WALMode     bool
	BusyTimeout time.Duration
	MaxConns    int
}

type Store struct {
	config StoreConfig
	mgmtDB *sql.DB
	mu     sync.RWMutex
	dbs    map[string]*Database
}

type Database struct {
	Name      string
	Label     string
	Username  string
	Password  string
	Status    string
	CreatedAt time.Time
	Pool      *sqlitepkg.Pool
}

type Details struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (d *Database) Details() Details {
	return Details{
		Name:      d.Name,
		Label:     d.Label,
		Username:  d.Username,
		Password:  d.Password,
		Status:    d.Status,
		CreatedAt: d.CreatedAt.Format(time.RFC3339),
	}
}

func Open(cfg StoreConfig) (*Store, error) {
	if cfg.Root == "" {
		return nil, errors.New("storage root is required")
	}
	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 10
	}
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = 5 * time.Second
	}

	mgmtPath := filepath.Join(cfg.Root, "management.db")
	mgmtDSN := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on",
		mgmtPath, int(cfg.BusyTimeout.Milliseconds()))
	mgmtDB, err := sql.Open("sqlite3", mgmtDSN)
	if err != nil {
		return nil, fmt.Errorf("open management DB: %w", err)
	}
	mgmtDB.SetMaxOpenConns(1)
	mgmtDB.SetMaxIdleConns(1)

	if err := mgmtDB.Ping(); err != nil {
		mgmtDB.Close()
		return nil, fmt.Errorf("ping management DB: %w", err)
	}

	if _, err := mgmtDB.Exec(`CREATE TABLE IF NOT EXISTS databases (
		name TEXT PRIMARY KEY,
		label TEXT NOT NULL DEFAULT '',
		username TEXT UNIQUE NOT NULL,
		password TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ready',
		created_at TEXT NOT NULL
	)`); err != nil {
		mgmtDB.Close()
		return nil, fmt.Errorf("create management schema: %w", err)
	}

	s := &Store{
		config: cfg,
		mgmtDB: mgmtDB,
		dbs:    make(map[string]*Database),
	}

	if err := s.reopenTenants(); err != nil {
		mgmtDB.Close()
		return nil, fmt.Errorf("reopen tenants: %w", err)
	}

	return s, nil
}

func (s *Store) reopenTenants() error {
	rows, err := s.mgmtDB.Query(`SELECT name, label, username, password, status, created_at FROM databases`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name, label, username, password, status, createdAtStr string
		if err := rows.Scan(&name, &label, &username, &password, &status, &createdAtStr); err != nil {
			return err
		}
		createdAt, _ := time.Parse(time.RFC3339, createdAtStr)

		tenantPath := filepath.Join(s.config.Root, name+".sqlite")
		pool, err := openTenantPool(tenantPath, s.config)
		if err != nil {
			continue
		}

		s.dbs[name] = &Database{
			Name:      name,
			Label:     label,
			Username:  username,
			Password:  password,
			Status:    status,
			CreatedAt: createdAt,
			Pool:      pool,
		}
	}
	return rows.Err()
}

func openTenantPool(path string, cfg StoreConfig) (*sqlitepkg.Pool, error) {
	return sqlitepkg.NewPool(path, sqlitepkg.PoolConfig{
		MaxOpenConns: cfg.MaxConns,
		MaxIdleConns: cfg.MaxConns / 2,
		MaxLifetime:  time.Hour,
		BusyTimeout:  cfg.BusyTimeout,
		WALMode:      cfg.WALMode,
		Restricted:   true,
	})
}

func (s *Store) Root() string {
	return s.config.Root
}

func (s *Store) Create(label string) (*Database, error) {
	if len(label) > 255 {
		return nil, ErrInvalidLabel
	}

	now := time.Now().UTC()
	name := "db_" + ulid.Make().String()
	username := "u_" + ulid.Make().String()
	password := generatePassword(32)

	tenantPath := filepath.Join(s.config.Root, name+".sqlite")
	pool, err := openTenantPool(tenantPath, s.config)
	if err != nil {
		return nil, fmt.Errorf("create tenant pool: %w", err)
	}

	createdAt := now.Format(time.RFC3339)
	if _, err := s.mgmtDB.Exec(
		`INSERT INTO databases (name, label, username, password, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		name, label, username, password, StatusReady, createdAt,
	); err != nil {
		pool.Close()
		os.Remove(tenantPath)
		return nil, fmt.Errorf("insert catalog: %w", err)
	}

	db := &Database{
		Name:      name,
		Label:     label,
		Username:  username,
		Password:  password,
		Status:    StatusReady,
		CreatedAt: now,
		Pool:      pool,
	}

	s.mu.Lock()
	s.dbs[name] = db
	s.mu.Unlock()

	return db, nil
}

func (s *Store) List() ([]Details, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Details, 0, len(s.dbs))
	for _, db := range s.dbs {
		d := db.Details()
		d.Password = ""
		result = append(result, d)
	}
	return result, nil
}

func (s *Store) Get(name string) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	db, ok := s.dbs[name]
	if !ok {
		return nil, ErrNotFound
	}
	return db, nil
}

func (s *Store) GetByUsername(username string) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, db := range s.dbs {
		if db.Username == username {
			return db, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	for _, db := range s.dbs {
		if err := db.Pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.dbs = make(map[string]*Database)

	if s.mgmtDB != nil {
		if err := s.mgmtDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.mgmtDB = nil
	}
	return firstErr
}

func (s *Store) Stats() map[string]sql.DBStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]sql.DBStats, len(s.dbs))
	for _, db := range s.dbs {
		stats[db.Name] = db.Pool.Stats()
	}
	return stats
}

func generatePassword(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)
}
