package server

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/dot5enko/sqliteproxy/storage"
)

// Session represents a client connection session
type Session struct {
	ID           string
	Username     string
	Database     string
	ConnectedAt  time.Time
	LastActiveAt time.Time
	Variables    map[string]string

	// Bound tenant database (immutable after auth)
	BoundDB *storage.Database

	// Transaction state
	InTransaction bool
	Tx            *sql.Tx

	// Per-session database connection for coherency
	DB *sql.Conn

	mu sync.RWMutex
}

// NewSession creates a new unbound session
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:           id,
		ConnectedAt:  now,
		LastActiveAt: now,
		Variables:    make(map[string]string),
	}
}

// Bind permanently attaches the session to an authenticated database.
func (s *Session) Bind(db *storage.Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.BoundDB != nil {
		if s.BoundDB.Name != db.Name {
			return fmt.Errorf("session already bound to a different database")
		}
		return nil
	}

	s.BoundDB = db
	s.Username = db.Username
	s.Database = db.Name
	s.LastActiveAt = time.Now()
	return nil
}

// GetBoundDB returns the authenticated tenant database.
func (s *Session) GetBoundDB() *storage.Database {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.BoundDB
}

// SetVariable sets a session variable
func (s *Session) SetVariable(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Variables[key] = value
	s.LastActiveAt = time.Now()
}

// GetVariable gets a session variable
func (s *Session) GetVariable(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.Variables[key]
	return val, ok
}

// SetDatabase sets the current database name label (must match bound DB).
func (s *Session) SetDatabase(db string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Database = db
	s.LastActiveAt = time.Now()
}

// GetDatabase returns the current database
func (s *Session) GetDatabase() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Database
}

// BeginTransaction starts a transaction on this session
func (s *Session) BeginTransaction(tx *sql.Tx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InTransaction = true
	s.Tx = tx
	s.LastActiveAt = time.Now()
}

// CommitTransaction commits the current transaction
func (s *Session) CommitTransaction() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Tx == nil {
		return fmt.Errorf("no active transaction")
	}

	err := s.Tx.Commit()
	s.Tx = nil
	s.InTransaction = false
	s.LastActiveAt = time.Now()
	return err
}

// RollbackTransaction rolls back the current transaction
func (s *Session) RollbackTransaction() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Tx == nil {
		return fmt.Errorf("no active transaction")
	}

	err := s.Tx.Rollback()
	s.Tx = nil
	s.InTransaction = false
	s.LastActiveAt = time.Now()
	return err
}

// IsInTransaction returns whether the session is in a transaction
func (s *Session) IsInTransaction() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.InTransaction
}

// GetTransaction returns the current transaction
func (s *Session) GetTransaction() *sql.Tx {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Tx
}

// SetConnection sets the per-session database connection
func (s *Session) SetConnection(conn *sql.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DB = conn
}

// GetConnection returns the per-session database connection
func (s *Session) GetConnection() *sql.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DB
}

// Close cleans up session resources
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error

	if s.Tx != nil {
		if err := s.Tx.Rollback(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.Tx = nil
		s.InTransaction = false
	}

	if s.DB != nil {
		if err := s.DB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.DB = nil
	}

	return firstErr
}

// SessionManager manages active sessions
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// Create creates a new unbound session
func (sm *SessionManager) Create(id string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := NewSession(id)
	sm.sessions[id] = session

	fmt.Printf("[INFO] Session created: %s\n", id)
	return session
}

// Get returns a session by ID
func (sm *SessionManager) Get(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[id]
	return session, ok
}

// Remove removes a session
func (sm *SessionManager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[id]; ok {
		session.Close()
		delete(sm.sessions, id)
		fmt.Printf("[INFO] Session removed: %s\n", id)
	}
}

// Count returns the number of active sessions
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// Cleanup removes inactive sessions
func (sm *SessionManager) Cleanup(maxInactive time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	removed := 0

	for id, session := range sm.sessions {
		if now.Sub(session.LastActiveAt) > maxInactive {
			session.Close()
			delete(sm.sessions, id)
			removed++
		}
	}

	if removed > 0 {
		fmt.Printf("[INFO] Cleaned up %d inactive sessions\n", removed)
	}

	return removed
}
