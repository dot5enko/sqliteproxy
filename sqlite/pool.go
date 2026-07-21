package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-sqlite3"
)

const (
	// DriverName is the unrestricted sqlite3 driver used for the management catalog.
	DriverName = "sqlite3"
	// RestrictedDriverName denies ATTACH/DETACH and unsafe PRAGMAs for tenant databases.
	RestrictedDriverName = "sqlite3_restricted"
)

var registerRestrictedOnce sync.Once

func init() {
	registerRestrictedDriver()
}

func registerRestrictedDriver() {
	registerRestrictedOnce.Do(func() {
		sql.Register(RestrictedDriverName, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				conn.RegisterAuthorizer(func(op int, arg1, arg2, arg3 string) int {
					switch op {
					case sqlite3.SQLITE_ATTACH, sqlite3.SQLITE_DETACH:
						return sqlite3.SQLITE_DENY
					case sqlite3.SQLITE_PRAGMA:
						name := strings.ToLower(arg1)
						switch name {
						case "writable_schema", "load_extension", "vdbe_debug",
							"journal_mode", "busy_timeout", "foreign_keys",
							"table_info", "index_list", "index_info", "database_list",
							"compile_options", "integrity_check", "quick_check",
							"foreign_key_list", "table_xinfo", "collation_list",
							"function_list", "module_list", "pragma_list",
							"schema_version", "user_version", "data_version",
							"page_count", "page_size", "freelist_count",
							"encoding", "cache_size", "synchronous",
							"temp_store", "locking_mode", "wal_checkpoint",
							"application_id", "auto_vacuum", "secure_delete":
							return sqlite3.SQLITE_OK
						default:
							return sqlite3.SQLITE_DENY
						}
					case sqlite3.SQLITE_FUNCTION:
						if strings.EqualFold(arg2, "load_extension") {
							return sqlite3.SQLITE_DENY
						}
					}
					return sqlite3.SQLITE_OK
				})
				return nil
			},
		})
	})
}

// PoolConfig holds configuration for the SQLite connection pool
type PoolConfig struct {
	MaxOpenConns int
	MaxIdleConns int
	MaxLifetime  time.Duration
	BusyTimeout  time.Duration
	WALMode      bool
	// Restricted enables the ATTACH/DETACH/PRAGMA authorizer. Tenant DBs should use this.
	Restricted bool
}

// DefaultPoolConfig returns sensible defaults
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns: 10,
		MaxIdleConns: 5,
		MaxLifetime:  time.Hour,
		BusyTimeout:  5 * time.Second,
		WALMode:      true,
		Restricted:   false,
	}
}

// Pool manages SQLite database connections
type Pool struct {
	db     *sql.DB
	config PoolConfig
}

// NewPool creates a new SQLite connection pool
func NewPool(dbPath string, config PoolConfig) (*Pool, error) {
	driverName := DriverName
	if config.Restricted {
		driverName = RestrictedDriverName
	}

	dsn := fmt.Sprintf("file:%s?_journal_mode=%s&_busy_timeout=%d&_foreign_keys=on",
		dbPath,
		walMode(config.WALMode),
		int(config.BusyTimeout.Milliseconds()),
	)

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.MaxLifetime)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}

	if config.WALMode {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
		}
	}

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
	if p == nil || p.db == nil {
		return nil
	}
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
