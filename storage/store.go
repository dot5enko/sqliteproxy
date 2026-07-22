package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/oklog/ulid/v2"

	"github.com/dot5enko/sqliteproxy/sqlite"
)

var (
	ErrNotFound     = errors.New("database not found")
	ErrInvalidLabel = errors.New("invalid label")
	ErrClosed       = errors.New("store is closed")
)

const (
	managementDBName = "management.db"
	maxLabelLen      = 128
)

// StoreConfig configures the storage root and per-database pools.
type StoreConfig struct {
	Root        string
	WALMode     bool
	BusyTimeout time.Duration
	MaxConns    int
}

// Store owns management.db and ready tenant database pools.
type Store struct {
	root   string
	config StoreConfig
	mgmt   *sql.DB

	mu       sync.RWMutex
	byName   map[string]*Database
	byUser   map[string]*Database
	closed   bool
	createMu sync.Mutex
}

// Open creates or opens the storage root and loads ready databases.
func Open(config StoreConfig) (*Store, error) {
	if config.Root == "" {
		return nil, fmt.Errorf("storage root is required")
	}
	if config.MaxConns <= 0 {
		config.MaxConns = 10
	}
	if config.BusyTimeout <= 0 {
		config.BusyTimeout = 5 * time.Second
	}

	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}

	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("storage root must be a directory: %s", root)
	}

	mgmtPath := filepath.Join(root, managementDBName)
	mgmt, err := sql.Open("sqlite3", fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on",
		mgmtPath,
		int(config.BusyTimeout.Milliseconds()),
	))
	if err != nil {
		return nil, fmt.Errorf("open management db: %w", err)
	}
	mgmt.SetMaxOpenConns(1)
	mgmt.SetMaxIdleConns(1)

	if err := mgmt.Ping(); err != nil {
		mgmt.Close()
		return nil, fmt.Errorf("ping management db: %w", err)
	}
	_ = os.Chmod(mgmtPath, 0o600)

	if err := migrate(mgmt); err != nil {
		mgmt.Close()
		return nil, err
	}

	s := &Store{
		root:   root,
		config: config,
		mgmt:   mgmt,
		byName: make(map[string]*Database),
		byUser: make(map[string]*Database),
	}

	if err := s.reconcile(); err != nil {
		s.Close()
		return nil, err
	}

	if err := s.loadReady(); err != nil {
		s.Close()
		return nil, err
	}

	return s, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS databases (
			name TEXT PRIMARY KEY NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('creating', 'ready')),
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_databases_username ON databases(username);
	`)
	if err != nil {
		return fmt.Errorf("migrate management schema: %w", err)
	}
	return nil
}

func (s *Store) reconcile() error {
	rows, err := s.mgmt.Query(`SELECT name FROM databases WHERE status = ?`, StatusCreating)
	if err != nil {
		return fmt.Errorf("list creating databases: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range names {
		path := s.dbPath(name)
		_ = os.Remove(path)
		_ = os.Remove(path + "-wal")
		_ = os.Remove(path + "-shm")
		if _, err := s.mgmt.Exec(`DELETE FROM databases WHERE name = ? AND status = ?`, name, StatusCreating); err != nil {
			return fmt.Errorf("cleanup creating database %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) loadReady() error {
	rows, err := s.mgmt.Query(`
		SELECT name, label, username, password, status, created_at
		FROM databases
		WHERE status = ?
	`, StatusReady)
	if err != nil {
		return fmt.Errorf("list ready databases: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rec Record
		var created string
		if err := rows.Scan(&rec.Name, &rec.Label, &rec.Username, &rec.Password, &rec.Status, &created); err != nil {
			return err
		}
		ts, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, created)
			if err != nil {
				return fmt.Errorf("parse created_at for %s: %w", rec.Name, err)
			}
		}
		rec.CreatedAt = ts

		path := s.dbPath(rec.Name)
		if _, err := os.Lstat(path); err != nil {
			return fmt.Errorf("ready database file missing for %s: %w", rec.Name, err)
		}

		pool, err := s.openTenantPool(path)
		if err != nil {
			return fmt.Errorf("open ready database %s: %w", rec.Name, err)
		}

		db := &Database{
			Record: rec,
			Path:   path,
			Pool:   pool,
		}
		s.mu.Lock()
		err = s.publishLocked(db)
		s.mu.Unlock()
		if err != nil {
			pool.Close()
			return err
		}
	}
	return rows.Err()
}

func (s *Store) publishLocked(db *Database) error {
	if _, exists := s.byName[db.Name]; exists {
		return fmt.Errorf("duplicate database name in catalog: %s", db.Name)
	}
	if _, exists := s.byUser[db.Username]; exists {
		return fmt.Errorf("duplicate username in catalog: %s", db.Username)
	}
	s.byName[db.Name] = db
	s.byUser[db.Username] = db
	return nil
}

func (s *Store) openTenantPool(path string) (*sqlite.Pool, error) {
	return sqlite.NewPool(path, sqlite.PoolConfig{
		MaxOpenConns: s.config.MaxConns,
		MaxIdleConns: max(1, s.config.MaxConns/2),
		MaxLifetime:  time.Hour,
		BusyTimeout:  s.config.BusyTimeout,
		WALMode:      s.config.WALMode,
		Restricted:   true,
	})
}

func (s *Store) dbPath(name string) string {
	return filepath.Join(s.root, name+".sqlite")
}

// Root returns the absolute storage root.
func (s *Store) Root() string {
	return s.root
}

// Create provisions a new tenant database.
func (s *Store) Create(label string) (*Database, error) {
	label, err := normalizeLabel(label)
	if err != nil {
		return nil, err
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}

	name := "db_" + strings.ToLower(ulid.Make().String())
	username := "u_" + strings.ToLower(ulid.Make().String())
	password, err := generatePassword()
	if err != nil {
		return nil, err
	}
	createdAt := time.Now().UTC()

	tx, err := s.mgmt.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO databases (name, label, username, password, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, name, label, username, password, StatusCreating, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("reserve database metadata: %w", err)
	}

	path := s.dbPath(name)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}

	validatePool, err := s.openTenantPool(path)
	if err != nil {
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
		return nil, fmt.Errorf("initialize database file: %w", err)
	}
	if err := validatePool.Close(); err != nil {
		return nil, err
	}

	_, err = tx.Exec(`UPDATE databases SET status = ? WHERE name = ?`, StatusReady, name)
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("mark database ready: %w", err)
	}
	if err := tx.Commit(); err != nil {
		os.Remove(path)
		return nil, err
	}

	pool, err := s.openTenantPool(path)
	if err != nil {
		return nil, fmt.Errorf("open ready database pool: %w", err)
	}

	db := &Database{
		Record: Record{
			Name:      name,
			Label:     label,
			Username:  username,
			Password:  password,
			Status:    StatusReady,
			CreatedAt: createdAt,
		},
		Path: path,
		Pool: pool,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		pool.Close()
		return nil, ErrClosed
	}
	if err := s.publishLocked(db); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// List returns ready database summaries without passwords.
func (s *Store) List() ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}

	out := make([]Summary, 0, len(s.byName))
	for _, db := range s.byName {
		out = append(out, db.Summary())
	}
	return out, nil
}

// Get returns a ready database by generated name.
func (s *Store) Get(name string) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	db, ok := s.byName[name]
	if !ok {
		return nil, ErrNotFound
	}
	return db, nil
}

// GetByUsername returns a ready database by MySQL username.
func (s *Store) GetByUsername(username string) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	db, ok := s.byUser[username]
	if !ok {
		return nil, ErrNotFound
	}
	return db, nil
}

// Close closes all tenant pools and the management database.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	for _, db := range s.byName {
		if err := db.Pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.byName = nil
	s.byUser = nil

	if s.mgmt != nil {
		if err := s.mgmt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.mgmt = nil
	}
	return firstErr
}

// Stats returns per-database pool stats.
func (s *Store) Stats() map[string]sql.DBStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]sql.DBStats, len(s.byName))
	for name, db := range s.byName {
		out[name] = db.Pool.Stats()
	}
	return out
}

func normalizeLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", nil
	}
	if len(label) > maxLabelLen {
		return "", fmt.Errorf("%w: max length is %d", ErrInvalidLabel, maxLabelLen)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: control characters are not allowed", ErrInvalidLabel)
		}
	}
	return label, nil
}

func generatePassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
