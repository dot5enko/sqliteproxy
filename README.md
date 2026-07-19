# SQLite Wire Proxy

A MySQL-compatible SQLite server that allows CGO-free Go binaries to access SQLite databases over TCP using the MySQL wire protocol.

## Why?

Go binaries using SQLite require CGO because the `mattn/go-sqlite3` driver is a CGO wrapper around the C SQLite library. This proxy moves the CGO dependency into a separate process, allowing your main binary to remain pure Go while still using SQLite through a standard MySQL client.

```
┌─────────────────┐         ┌──────────────────────────┐         ┌─────────────┐
│   Go Binary     │  TCP    │   sqlite-wire-proxy      │  CGO    │   SQLite    │
│  (no CGO)       │────────▶│   (MySQL protocol :3306) │────────▶│   .db file  │
│  go-sql-driver  │         │   + SQL translator       │         │             │
│  /mysql         │         │   + Auth / Sessions      │         │             │
└─────────────────┘         └──────────────────────────┘         └─────────────┘
```

## Quick Start

### 1. Start the proxy

```bash
# Basic usage
./bin/sqlite-proxy -db ./mydata.sqlite -port 3306

# With authentication
./bin/sqlite-proxy -db ./mydata.sqlite -port 3306 -user admin -password secret

# With config file
./bin/sqlite-proxy -config config.example.json
```

### 2. Connect with the included CLI client

```bash
# Basic connection
./bin/sqlite-client -h 127.0.0.1 -P 3306 -u admin -p secret

# With debug timing
./bin/sqlite-client -h 127.0.0.1 -P 3306 -u admin -p secret --debug

# Select database
./bin/sqlite-client -h 127.0.0.1 -P 3306 -u admin -p secret mydb
```

### 3. Or connect with MySQL client

```bash
mysql -h 127.0.0.1 -P 3306 -u admin -psecret
```

### CLI Client Features

The included `sqlite-client` provides a MySQL-like interactive shell:

```
sqlite> SELECT * FROM users;
+----+---------+---------------------+
| id | name    | email               |
+----+---------+---------------------+
| 1  | Alice   | alice@example.com   |
| 2  | Bob     | bob@example.com     |
| 3  | Charlie | charlie@example.com |
+----+---------+---------------------+
3 rows in set (518µs)
```

Commands:
- `help` or `\h` - Show help
- `quit`, `exit`, or `\q` - Exit
- `show tables` or `\t` - List tables
- `DESCRIBE table` - Show table structure
- `status` or `\s` - Show connection status
- `--debug` flag - Show query execution time

### 3. Connect from Go (no CGO required)

```go
import (
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
)

// IMPORTANT: Use interpolateParams=true to avoid prepared statement issues
db, err := sql.Open("mysql", "admin:secret@tcp(127.0.0.1:3306)/mydata.sqlite?interpolateParams=true")

// Use like any normal database/sql connection
rows, err := db.Query("SELECT id, name FROM users WHERE active = ?", true)
```

## Architecture

```
packages/sqlite/
├── cmd/sqlite-proxy/       # CLI binary entrypoint
│   └── main.go
├── server/                 # MySQL wire protocol handling
│   ├── handler.go          # Query execution & result building
│   ├── auth.go             # Authentication provider
│   └── session.go          # Connection session management
├── sqlite/                 # SQLite database layer
│   └── pool.go             # WAL-mode connection pool
├── translator/             # SQL dialect translation
│   ├── translator.go       # MySQL → SQLite SQL rewriter
│   └── translator_test.go  # Unit tests
├── tests/
│   └── integration_test.go # End-to-end tests
├── config.go               # Configuration structs
├── config.example.json     # Example config file
└── proxy.go                # Main orchestrator
```

### Components

| Component | Responsibility |
|-----------|---------------|
| **proxy.go** | Wires everything together: listener, handler, pool |
| **server/handler.go** | Implements `go-mysql-org/go-mysql/server.Handler` interface. Receives MySQL commands, translates SQL, executes on SQLite, returns MySQL-formatted results |
| **server/auth.go** | Validates username/password credentials using MySQL's `mysql_native_password` authentication |
| **server/session.go** | Tracks active connections, session variables, and handles cleanup |
| **sqlite/pool.go** | Manages SQLite connections with WAL journal mode for concurrent reads |
| **translator/translator.go** | Rewrites MySQL SQL syntax to SQLite-compatible SQL |

## SQL Translation

The translator converts MySQL-specific SQL to SQLite equivalents:

### DDL (Schema)

| MySQL | SQLite |
|-------|--------|
| `INT AUTO_INCREMENT` | `INTEGER` (implicit ROWID) |
| `VARCHAR(n)` | `TEXT` |
| `DATETIME` | `TEXT` |
| `BOOLEAN` | `INTEGER` |
| `ENGINE=InnoDB` | Removed |
| `DEFAULT CHARSET=utf8mb4` | Removed |

### DML (Queries)

| MySQL | SQLite |
|-------|--------|
| `LIMIT offset, count` | `LIMIT count OFFSET offset` |
| `NOW()` | `datetime('now')` |
| `CURDATE()` | `date('now')` |
| `UNIX_TIMESTAMP()` | `strftime('%s','now')` |
| `TRUE` / `FALSE` | `1` / `0` |
| Backticks `` ` `` | Double quotes `"` |

### Meta Commands

| MySQL | SQLite |
|-------|--------|
| `SHOW TABLES` | `SELECT name FROM sqlite_master WHERE type='table'` |
| `SHOW DATABASES` | `SELECT 'main' AS Database` |
| `DESCRIBE table` | `PRAGMA table_info(table)` |
| `SET NAMES utf8mb4` | `SELECT 1` (no-op) |
| `USE database` | `SELECT 1` (no-op) |

## Configuration

### Command Line Flags

```
-address string     Listen address (default "0.0.0.0")
-config string      Path to configuration file (JSON)
-db string          SQLite database path (default "./data.sqlite")
-debug              Enable debug logging
-max-conns int      Maximum database connections (default 10)
-password string    Password for authentication
-port int           Listen port (default 3306)
-user string        Username for authentication (empty = no auth)
-wal                Enable WAL mode (default true)
```

### Config File (JSON)

```json
{
  "server": {
    "address": "0.0.0.0",
    "port": 3306
  },
  "database": {
    "path": "./data.sqlite",
    "wal_mode": true,
    "busy_timeout_ms": 5000,
    "max_connections": 10
  },
  "auth": {
    "username": "admin",
    "password": "secret"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

## Building

```bash
# Build the binary
cd packages/sqlite
go build -o bin/sqlite-proxy ./cmd/sqlite-proxy/

# Run tests
go test ./translator/ -v
go test ./tests/ -v
```

## SQLite Features

### WAL Mode

The proxy enables WAL (Write-Ahead Logging) mode by default, which allows:
- Multiple concurrent readers
- One writer (non-blocking reads during writes)
- Better performance for concurrent workloads

### Connection Pool

Configurable connection pool manages concurrent access:
- `max_connections`: Maximum open connections (default 10)
- Connections are reused automatically
- Busy timeout prevents lock contention errors

### Transaction Support

The proxy translates MySQL transaction commands to SQLite:

| MySQL | SQLite |
|-------|--------|
| `START TRANSACTION` | `BEGIN TRANSACTION` |
| `BEGIN` | `BEGIN TRANSACTION` |
| `COMMIT` | `COMMIT` |
| `ROLLBACK` | `ROLLBACK` |
| `SAVEPOINT name` | `SAVEPOINT name` (native) |
| `RELEASE SAVEPOINT name` | `RELEASE SAVEPOINT name` (native) |
| `ROLLBACK TO SAVEPOINT name` | `ROLLBACK TO SAVEPOINT name` (native) |

Example:
```sql
START TRANSACTION;
INSERT INTO users (name) VALUES ('Alice');
INSERT INTO users (name) VALUES ('Bob');
COMMIT;

-- Or with savepoints
START TRANSACTION;
INSERT INTO users (name) VALUES ('Charlie');
SAVEPOINT sp1;
INSERT INTO users (name) VALUES ('David');
ROLLBACK TO SAVEPOINT sp1; -- David is undone, Charlie remains
COMMIT;
```

### Session Coherency

Each client connection maintains its own session state:
- Session variables are tracked per connection
- Transaction state is maintained per session
- Database context (`USE database`) is tracked per session
- Session cleanup happens automatically on disconnect

## Limitations

### Prepared Statements

The proxy has limited support for MySQL prepared statements. Use `interpolateParams=true` in your Go DSN to use the text protocol instead:

```go
db, _ := sql.Open("mysql", "user:pass@tcp(host:3306)/db?interpolateParams=true")
```

### SQL Compatibility

Not all MySQL features are supported. The translator handles common CRUD operations and GORM-generated queries. Unsupported features:
- Stored procedures
- Triggers (MySQL syntax)
- `ON DUPLICATE KEY UPDATE` (removed, not translated)
- Full-text search
- Some JSON functions

### Single Database

SQLite doesn't support multiple databases in the same way MySQL does. The `USE database` command is a no-op.

## Use Cases

- **CGO-free Go binaries**: Access SQLite without CGO in your main binary
- **Development**: Use familiar MySQL tools with SQLite databases
- **Testing**: Lightweight SQL database for test environments
- **Migration**: Gradually move from MySQL to SQLite or vice versa

## Dependencies

- `github.com/go-mysql-org/go-mysql` - MySQL wire protocol server
- `github.com/mattn/go-sqlite3` - SQLite driver (CGO, only in proxy)
- `github.com/go-sql-driver/mysql` - MySQL client driver (for Go consumers)

## License

Part of the cloudfunctions-core project.
