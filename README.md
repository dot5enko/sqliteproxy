# SQLite Wire Proxy

A multi-tenant MySQL-compatible SQLite service. Provision databases over HTTP, then connect with any MySQL client using generated credentials. Each database is an isolated SQLite file under a single storage volume.

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
./bin/sqlite-proxy -storage ./storage -port 3306 -management-port 8080
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

## Storage layout

```text
./storage/
├── management.db              # authoritative catalog + credentials
├── db_<ulid>.sqlite           # tenant database
├── db_<ulid>.sqlite-wal
└── db_<ulid>.sqlite-shm
```

`management.db` is the sole source of truth. There are no per-database JSON files. Mount `./storage` as a single Docker volume.

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
| `-storage` | `./storage` | Storage root |
| `-address` | `0.0.0.0` | MySQL listen address |
| `-port` | `3306` | MySQL listen port |
| `-management-address` | `127.0.0.1` | Management HTTP address |
| `-management-port` | `8080` | Management HTTP port |
| `-wal` | `true` | WAL mode for tenant DBs |
| `-max-conns` | `10` | Max connections **per** tenant DB |
| `-config` | | JSON config file |
| `-debug` | `false` | Debug logging |

The old single-file `-db` / `-user` / `-password` flags have been removed.

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

## SQL compatibility

MySQL dialect is translated to SQLite (types, `AUTO_INCREMENT`, `SHOW TABLES`, `DESCRIBE`, common functions, transactions). Gaps remain for stored procedures, full-text search, and many JSON helpers. Prefer the text protocol (`interpolateParams=true`).

## Development

```bash
go test ./...
go test -race ./...
```

## License

MIT
