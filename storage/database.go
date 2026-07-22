package storage

import (
	"time"

	"github.com/dot5enko/sqliteproxy/sqlite"
)

const (
	StatusCreating = "creating"
	StatusReady    = "ready"
)

// Record is the durable metadata for a managed database.
type Record struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	Username  string    `json:"username"`
	Password  string    `json:"password,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Summary is a list view without credentials.
type Summary struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	Username  string    `json:"username"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Details is an inspect/create response that may include credentials.
type Details struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	Username  string    `json:"username"`
	Password  string    `json:"password,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Database is a ready runtime entry with an open pool.
type Database struct {
	Record
	Path string
	Pool *sqlite.Pool
}

// Summary returns list metadata without password.
func (d *Database) Summary() Summary {
	return Summary{
		Name:      d.Name,
		Label:     d.Label,
		Username:  d.Username,
		Status:    d.Status,
		CreatedAt: d.CreatedAt,
	}
}

// Details returns inspect metadata including password.
func (d *Database) Details() Details {
	return Details{
		Name:      d.Name,
		Label:     d.Label,
		Username:  d.Username,
		Password:  d.Password,
		Status:    d.Status,
		CreatedAt: d.CreatedAt,
	}
}
