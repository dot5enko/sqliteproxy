package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dot5enko/cloudfunctions/packages/sqlite"
	"github.com/dot5enko/cloudfunctions/packages/sqlite/storage"
)

type createdDB struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func startTestProxy(t *testing.T) *sqlite.Proxy {
	t.Helper()
	config := sqlite.DefaultConfig()
	config.StorageRoot = filepath.Join(t.TempDir(), "storage")
	config.Address = "127.0.0.1"
	config.Port = 0
	config.ManagementAddress = "127.0.0.1"
	config.ManagementPort = 0
	config.MaxConns = 5

	proxy, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Stop() })

	// Give listeners a moment.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if proxy.MySQLAddr() != "" && proxy.ManagementAddr() != "" {
			return proxy
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy addresses not ready")
	return proxy
}

func createDatabase(t *testing.T, proxy *sqlite.Proxy, label string) createdDB {
	t.Helper()
	body := []byte(`{}`)
	if label != "" {
		body = []byte(fmt.Sprintf(`{"label":%q}`, label))
	}
	url := "http://" + proxy.ManagementAddr() + "/v1/databases"
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, data)
	}
	var out createdDB
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return out
}

func openMySQL(t *testing.T, proxy *sqlite.Proxy, db createdDB) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?interpolateParams=true&parseTime=true",
		db.Username, db.Password, proxy.MySQLAddr(), db.Name)
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping mysql: %v", err)
	}
	return conn
}

func TestIntegration(t *testing.T) {
	proxy := startTestProxy(t)
	dbMeta := createDatabase(t, proxy, "users")
	db := openMySQL(t, proxy, dbMeta)

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255),
			active BOOLEAN DEFAULT TRUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	result, err := db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", "John Doe", "john@example.com")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := result.LastInsertId()
	if id != 1 {
		t.Errorf("expected last insert id 1, got %d", id)
	}

	var name, email string
	var active bool
	err = db.QueryRow("SELECT name, email, active FROM users WHERE id = ?", 1).Scan(&name, &email, &active)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "John Doe" || email != "john@example.com" {
		t.Fatalf("unexpected row: %s %s", name, email)
	}

	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		t.Fatalf("show tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, table)
	}
	if len(tables) != 1 || tables[0] != "users" {
		t.Fatalf("expected [users], got %v", tables)
	}

	rows, err = db.Query("SHOW DATABASES")
	if err != nil {
		t.Fatalf("show databases: %v", err)
	}
	defer rows.Close()
	var dbs []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan db: %v", err)
		}
		dbs = append(dbs, n)
	}
	if len(dbs) != 1 || dbs[0] != dbMeta.Name {
		t.Fatalf("expected only bound database, got %v", dbs)
	}
}

func TestTransactionIsolation(t *testing.T) {
	proxy := startTestProxy(t)
	meta := createDatabase(t, proxy, "tx")
	dbA := openMySQL(t, proxy, meta)
	dbB := openMySQL(t, proxy, meta)

	_, err := dbA.Exec(`
		CREATE TABLE IF NOT EXISTS tx_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := dbA.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO tx_users (name) VALUES (?)", "Alice"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count); err != nil {
		t.Fatalf("query in tx: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 in tx, got %d", count)
	}
	if err := dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count); err != nil {
		t.Fatalf("query other conn: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 before commit, got %d", count)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count); err != nil {
		t.Fatalf("query after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 after commit, got %d", count)
	}
}

func TestTransactionRollback(t *testing.T) {
	proxy := startTestProxy(t)
	meta := createDatabase(t, proxy, "rollback")
	dbA := openMySQL(t, proxy, meta)
	dbB := openMySQL(t, proxy, meta)

	_, err := dbA.Exec(`
		CREATE TABLE IF NOT EXISTS tx_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := dbA.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO tx_users (name) VALUES (?)", "Bob"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := dbA.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Bob").Scan(&count); err != nil {
		t.Fatalf("query after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 after rollback, got %d", count)
	}
	if err := dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Bob").Scan(&count); err != nil {
		t.Fatalf("other conn after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 on other conn, got %d", count)
	}
}

func TestMultiDatabaseIsolation(t *testing.T) {
	proxy := startTestProxy(t)
	a := createDatabase(t, proxy, "a")
	b := createDatabase(t, proxy, "b")
	dbA := openMySQL(t, proxy, a)
	dbB := openMySQL(t, proxy, b)

	_, err := dbA.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	_, err = dbB.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`)
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := dbA.Exec(`INSERT INTO items (id, name) VALUES (1, 'from-a')`); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if _, err := dbB.Exec(`INSERT INTO items (id, name) VALUES (1, 'from-b')`); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	var nameA, nameB string
	if err := dbA.QueryRow(`SELECT name FROM items WHERE id = 1`).Scan(&nameA); err != nil {
		t.Fatalf("query A: %v", err)
	}
	if err := dbB.QueryRow(`SELECT name FROM items WHERE id = 1`).Scan(&nameB); err != nil {
		t.Fatalf("query B: %v", err)
	}
	if nameA != "from-a" || nameB != "from-b" {
		t.Fatalf("cross-db leak: A=%s B=%s", nameA, nameB)
	}

	if _, err := dbA.Exec("USE " + b.Name); err == nil {
		t.Fatal("expected USE to other database to fail")
	}
}

func TestAuthRejectsCrossDatabaseAndBadPassword(t *testing.T) {
	proxy := startTestProxy(t)
	a := createDatabase(t, proxy, "auth-a")
	b := createDatabase(t, proxy, "auth-b")

	// Wrong password
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?interpolateParams=true",
		a.Username, "wrong-password", proxy.MySQLAddr(), a.Name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("expected wrong password to fail")
	}

	// Cross-database username/db combination
	dsn = fmt.Sprintf("%s:%s@tcp(%s)/%s?interpolateParams=true",
		a.Username, a.Password, proxy.MySQLAddr(), b.Name)
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open cross: %v", err)
	}
	defer db2.Close()
	if err := db2.Ping(); err == nil {
		t.Fatal("expected cross-database credentials to fail")
	}
}

func TestAttachDenied(t *testing.T) {
	proxy := startTestProxy(t)
	meta := createDatabase(t, proxy, "secure")
	db := openMySQL(t, proxy, meta)

	other := filepath.Join(t.TempDir(), "evil.sqlite")
	_, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS evil", other))
	if err == nil {
		t.Fatal("expected ATTACH to be denied")
	}
}

func TestRestartPersistsCredentials(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")

	config := sqlite.DefaultConfig()
	config.StorageRoot = root
	config.Address = "127.0.0.1"
	config.Port = 0
	config.ManagementAddress = "127.0.0.1"
	config.ManagementPort = 0

	proxy, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	meta := createDatabase(t, proxy, "persist")
	db := openMySQL(t, proxy, meta)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO t (id) VALUES (7)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()
	if err := proxy.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	proxy2, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("new2: %v", err)
	}
	if err := proxy2.Start(); err != nil {
		t.Fatalf("start2: %v", err)
	}
	defer proxy2.Stop()

	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?interpolateParams=true",
		meta.Username, meta.Password, proxy2.MySQLAddr(), meta.Name)
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	if err := db2.Ping(); err != nil {
		t.Fatalf("ping after restart: %v", err)
	}
	var id int
	if err := db2.QueryRow(`SELECT id FROM t`).Scan(&id); err != nil {
		t.Fatalf("query after restart: %v", err)
	}
	if id != 7 {
		t.Fatalf("expected id 7, got %d", id)
	}

	// Ensure management catalog still lists it.
	got, err := proxy2.Store().Get(meta.Name)
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if got.Username != meta.Username {
		t.Fatalf("catalog mismatch")
	}
	_ = storage.StatusReady
}
