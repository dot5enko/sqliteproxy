package server

import (
	"sync"

	"github.com/go-mysql-org/go-mysql/server"

	storage "github.com/dot5enko/sqliteproxy/storage"
)

// ConnectionBinding tracks handshake state for one MySQL connection.
// go-mysql may call UseDB before authentication completes, so binding is
// finalized only after a successful handshake.
type ConnectionBinding struct {
	mu          sync.Mutex
	requestedDB string
	candidate   *storage.Database
	finalized   bool
	database    *storage.Database
}

// SetRequestedDB records the database name from the handshake or COM_INIT_DB.
// Before finalization this only stores a request; after finalization it enforces
// immutable binding.
func (b *ConnectionBinding) SetRequestedDB(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finalized {
		if b.database == nil || name != b.database.Name {
			return errDBAccessDenied(name)
		}
		return nil
	}
	b.requestedDB = name
	return nil
}

// RequestedDB returns the database name requested during handshake, if any.
func (b *ConnectionBinding) RequestedDB() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requestedDB
}

// SetCandidate stores the authenticated database for later finalization.
func (b *ConnectionBinding) SetCandidate(db *storage.Database) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.candidate = db
}

// Finalize permanently binds the connection to the authenticated database.
func (b *ConnectionBinding) Finalize(username string) (*storage.Database, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.finalized {
		return b.database, nil
	}
	if b.candidate == nil || b.candidate.Username != username {
		return nil, errAccessDenied()
	}
	if b.requestedDB != "" && b.requestedDB != b.candidate.Name {
		return nil, errAccessDenied()
	}

	b.database = b.candidate
	b.finalized = true
	return b.database, nil
}

// Database returns the finalized database, if any.
func (b *ConnectionBinding) Database() *storage.Database {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.database
}

// CredentialProvider implements server.CredentialProvider using the storage registry.
type CredentialProvider struct {
	store   *storage.Store
	binding *ConnectionBinding
}

// NewCredentialProvider creates a per-connection credential provider.
func NewCredentialProvider(store *storage.Store, binding *ConnectionBinding) *CredentialProvider {
	return &CredentialProvider{
		store:   store,
		binding: binding,
	}
}

// CheckUsername reports whether the username exists.
func (p *CredentialProvider) CheckUsername(username string) (bool, error) {
	_, err := p.store.GetByUsername(username)
	if err == storage.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetCredential returns the password for username and stages the database candidate.
// Username/database mismatches intentionally look like authentication failures.
func (p *CredentialProvider) GetCredential(username string) (password string, found bool, err error) {
	db, err := p.store.GetByUsername(username)
	if err == storage.ErrNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	requested := p.binding.RequestedDB()
	if requested != "" && requested != db.Name {
		return "", false, nil
	}

	p.binding.SetCandidate(db)
	return db.Password, true, nil
}

var _ server.CredentialProvider = (*CredentialProvider)(nil)
