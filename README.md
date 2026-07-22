# SQLite Wire Proxy

A multi-tenant MySQL-compatible SQLite service. Provision databases over HTTP, then connect with any MySQL client using generated credentials. Each database is an isolated SQLite file under a single storage volume.

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

## Features

- **Multi-database storage** — one process exposes many tenant SQLite files
- **MySQL wire protocol** — connect with `mysql`, Go `database/sql`, ORMs, etc.
- **HTTP management API** — create, list, and inspect databases
- **Per-database credentials** — generated username/password permanently binds a connection to one DB
- **Concurrent tenants** — independent connection pools per database
- **WAL mode** — better concurrent read performance within a tenant
- **ATTACH denied** — SQLite authorizer blocks filesystem escape via `ATTACH`/`DETACH`

## Quick Start

### Build

```bash
go build -o bin/sqlite-proxy ./cmd/sqlite-proxy
go build -o bin/sqlite-client ./cmd/sqlite-client
```

`sqlite-proxy` requires CGO (`mattn/go-sqlite3`). `sqlite-client` is a pure-Go interactive MySQL client for the proxy.

### Run

```bash
./bin/sqlite-proxy -storage ./data -port 3306 -management-port 8080
```

Or with a config file:

```bash
./bin/sqlite-proxy -config config.example.json
```

### Create a database

```bash
curl -sX POST http://127.0.0.1:8080/v1/databases \
  -H 'Content-Type: application/json' \
  -d '{"label":"orders"}'
```

Example response:

```json
{
  "name": "db_01jabcdefghijklmnopqrs",
  "label": "orders",
  "username": "u_01jabcdefghijklmnopqrs",
  "password": "generated-secret",
  "status": "ready",
  "created_at": "2026-07-21T08:30:00Z"
}
```

### Connect

With the included interactive client:

```bash
./bin/sqlite-client \
  -h 127.0.0.1 -P 3306 \
  -u u_01jabcdefghijklmnopqrs \
  -p generated-secret \
  db_01jabcdefghijklmnopqrs
```

Or with a standard MySQL client:

```bash
mysql -h 127.0.0.1 -P 3306 \
  -u u_01jabcdefghijklmnopqrs \
  -p'generated-secret' \
  db_01jabcdefghijklmnopqrs
```

Go DSN (always use `interpolateParams=true`):

```text
u_01j...:generated-secret@tcp(127.0.0.1:3306)/db_01j...?interpolateParams=true
```

## sqlite-client

`sqlite-client` is a lightweight REPL that speaks the MySQL wire protocol to the proxy. It supports line editing, tab completion, multi-line input (`\` at end of line), and history in `~/.sqlite_history`.

```bash
./bin/sqlite-client [options] [database]
```

| Option | Description |
|--------|-------------|
| `-h`, `--host` | Server host (default `127.0.0.1`) |
| `-P`, `--port` | Server port (default `3306`) |
| `-u`, `--user` | Username from `POST /v1/databases` |
| `-p`, `--password` | Password from create/inspect |
| `--debug` | Print query execution time |
| `-V`, `--version` | Show version |
| `--help` | Show help |

Inside the shell:

| Command | Description |
|---------|-------------|
| `help` / `\h` | Show commands |
| `quit` / `exit` / `\q` | Exit |
| `status` / `\s` | Connection status |
| `version` / `\v` | Client version |
| `SHOW TABLES` / `\t` | List tables |
| SQL | Any supported MySQL/SQLite statement |

Example session:

```text
Connected to 127.0.0.1:3306 as u_01jabcdefghijklmnopqrs
Type 'help' for commands, 'quit' to exit.

sqlite> CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
Query OK, 0 rows affected

sqlite> INSERT INTO users (name) VALUES ('Ada');
Query OK, 1 row affected

sqlite> SELECT * FROM users;
+----+------+
| id | name |
+----+------+
| 1  | Ada  |
+----+------+
1 row in set

sqlite> quit
```

## Architecture

```
├── cmd/
│   ├── sqlite-proxy/         # CLI binary entrypoint
│   │   └── main.go
│   └── sqlite-client/        # Interactive MySQL client
│       └── main.go
├── server/                   # MySQL wire protocol handling
│   ├── handler.go            # Query execution & result building
│   ├── auth.go               # Authentication provider
│   ├── session.go            # Connection session management
│   └── errors.go             # Error code mapping
├── sqlite/                   # SQLite database layer
│   └── pool.go               # WAL-mode connection pool
├── storage/                  # Multi-tenant storage
│   ├── store.go              # Management DB catalog
│   └── database.go           # Per-tenant database lifecycle
├── management/               # HTTP management API
│   └── handler.go            # CRUD for tenant databases
├── translator/               # SQL dialect translation
│   └── translator.go         # MySQL → SQLite SQL rewriter
├── config.go                 # Configuration structs
└── proxy.go                  # Main orchestrator
```

### Components

| Component | Responsibility |
|-----------|---------------|
| **proxy.go** | Wires everything together: listener, handler, pool |
| **server/handler.go** | Implements `go-mysql-org/go-mysql/server.Handler`. Receives MySQL commands, translates SQL, executes on SQLite, returns MySQL-formatted results |
| **server/auth.go** | Validates username/password credentials using MySQL's `mysql_native_password` authentication |
| **server/session.go** | Tracks active connections, session variables, and handles cleanup |
| **storage/store.go** | Manages the `management.db` catalog — authoritative source of database names, labels, and credentials |
| **storage/database.go** | Per-tenant database lifecycle: creates SQLite files, manages `sql.DB` pools, enforces isolation |
| **sqlite/pool.go** | Manages SQLite connections with WAL journal mode for concurrent reads |
| **management/handler.go** | HTTP API for `POST / GET /v1/databases` |
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

## Storage layout

```text
./data/
├── management.db              # authoritative catalog + credentials
├── db_<ulid>.sqlite           # tenant database
├── db_<ulid>.sqlite-wal
└── db_<ulid>.sqlite-shm
```

`management.db` is the sole source of truth. There are no per-database JSON files. Mount `./data` as a single Docker volume.

## Management API

Default listen address is `127.0.0.1:8080` (loopback). **There is no authentication on the management API in this MVP.** Only expose it on a private network if you bind to `0.0.0.0`.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/databases` | Create a database (`{"label":"..."}` optional) |
| `GET` | `/v1/databases` | List databases (no passwords) |
| `GET` | `/v1/databases/{name}` | Inspect one database (includes password) |

Create and inspect responses set `Cache-Control: no-store`.

Anyone who can call the management API or read the storage volume can obtain MySQL credentials.

## Isolation model

- Generated MySQL username uniquely maps to one database
- Handshake database name must match (or may be omitted)
- After connect, `USE other_db` / `COM_INIT_DB` cannot switch tenants
- `SHOW DATABASES` returns only the bound database
- Each tenant has its own `sql.DB` pool (`-max-conns` is **per database**)
- Tenant connections deny `ATTACH` / `DETACH` and most unsafe PRAGMAs

This is process-local logical isolation, not OS sandboxing.

## Configuration

### CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `-storage` | `./data` | Storage root |
| `-address` | `0.0.0.0` | MySQL listen address |
| `-port` | `3306` | MySQL listen port |
| `-management-address` | `127.0.0.1` | Management HTTP address |
| `-management-port` | `8080` | Management HTTP port |
| `-wal` | `true` | WAL mode for tenant DBs |
| `-max-conns` | `10` | Max connections **per** tenant DB |
| `-config` | | JSON config file |
| `-debug` | `false` | Debug logging |

### Config file

See [`config.example.json`](config.example.json).

## Deployer notes

Typical Docker usage:

1. Run one `sqlite-proxy` container with a persistent volume on `/storage`
2. On tenant provision, `POST /v1/databases` and store the returned credentials
3. App containers connect to the proxy with those MySQL credentials — no per-tenant volume mounts

Constraints:

- Single writer process per storage volume (do not scale MySQL listeners across replicas sharing the same files)
- Back up `management.db` and all `db_*.sqlite` (+ WAL/SHM) together
- Keep the management API private

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

## Use Cases

- **CGO-free Go binaries**: Access SQLite without CGO in your main binary
- **Development**: Use familiar MySQL tools with SQLite databases
- **Testing**: Lightweight SQL database for test environments
- **Migration**: Gradually move from MySQL to SQLite or vice versa

## Dependencies

- `github.com/go-mysql-org/go-mysql` — MySQL wire protocol server
- `github.com/mattn/go-sqlite3` — SQLite driver (CGO, only in proxy)
- `github.com/go-sql-driver/mysql` — MySQL client driver (for Go consumers)

## License

MIT
