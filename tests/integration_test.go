package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/dot5enko/cloudfunctions/packages/sqlite"
)




func TestIntegration(t *testing.T) {
	// Create a temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite")

	// Create proxy
	config := sqlite.DefaultConfig()
	config.DatabasePath = dbPath
	config.Port = 13306 // Use non-standard port for testing
	config.Username = "testuser"
	config.Password = "testpass"

	proxy, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// Start proxy
	if err := proxy.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Connect with MySQL client
	// Use interpolateParams=true to use text protocol instead of prepared statements
	dsn := fmt.Sprintf("testuser:testpass@tcp(127.0.0.1:%d)/?interpolateParams=true", config.Port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer db.Close()

	// Test connection
	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping: %v", err)
	}

	// Create table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255),
			active BOOLEAN DEFAULT TRUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert data
	result, err := db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", "John Doe", "john@example.com")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	id, _ := result.LastInsertId()
	if id != 1 {
		t.Errorf("Expected last insert id 1, got %d", id)
	}

	// Query data
	var name, email string
	var active bool
	err = db.QueryRow("SELECT name, email, active FROM users WHERE id = ?", 1).Scan(&name, &email, &active)
	if err != nil {
		t.Fatalf("Failed to query: %v", err)
	}

	if name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", name)
	}
	if email != "john@example.com" {
		t.Errorf("Expected email 'john@example.com', got '%s'", email)
	}

	// Test SHOW TABLES
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		t.Fatalf("Failed to show tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("Failed to scan table: %v", err)
		}
		tables = append(tables, table)
	}

	if len(tables) != 1 || tables[0] != "users" {
		t.Errorf("Expected [users], got %v", tables)
	}

	// Test DESCRIBE
	rows, err = db.Query("DESCRIBE users")
	if err != nil {
		t.Fatalf("Failed to describe table: %v", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull int
		var dfltValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("Failed to scan column: %v", err)
		}
		columns = append(columns, name)
	}

	expectedColumns := []string{"id", "name", "email", "active", "created_at"}
	if len(columns) != len(expectedColumns) {
		t.Errorf("Expected %d columns, got %d: %v", len(expectedColumns), len(columns), columns)
	}

	// Test update
	_, err = db.Exec("UPDATE users SET name = ? WHERE id = ?", "Jane Doe", 1)
	if err != nil {
		t.Fatalf("Failed to update: %v", err)
	}

	// Verify update
	err = db.QueryRow("SELECT name FROM users WHERE id = ?", 1).Scan(&name)
	if err != nil {
		t.Fatalf("Failed to query after update: %v", err)
	}
	if name != "Jane Doe" {
		t.Errorf("Expected name 'Jane Doe', got '%s'", name)
	}

	// Test delete
	_, err = db.Exec("DELETE FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("Failed to delete: %v", err)
	}

	// Verify delete
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 rows, got %d", count)
	}

	fmt.Println("All integration tests passed!")
}

func TestTransactionIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite")

	config := sqlite.DefaultConfig()
	config.DatabasePath = dbPath
	config.Port = 13307
	config.Username = "testuser"
	config.Password = "testpass"

	proxy, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	if err := proxy.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	time.Sleep(100 * time.Millisecond)

	dsn := fmt.Sprintf("testuser:testpass@tcp(127.0.0.1:%d)/?interpolateParams=true", config.Port)
	openConn := func() *sql.DB {
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		if err := db.Ping(); err != nil {
			t.Fatalf("Failed to ping: %v", err)
		}
		return db
	}

	dbA := openConn()
	defer dbA.Close()
	dbB := openConn()
	defer dbB.Close()

	_, err = dbA.Exec(`
		CREATE TABLE IF NOT EXISTS tx_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	tx, err := dbA.Begin()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	_, err = tx.Exec("INSERT INTO tx_users (name) VALUES (?)", "Alice")
	if err != nil {
		t.Fatalf("Failed to insert in transaction: %v", err)
	}

	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query within transaction: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected same transaction to see 1 row, got %d", count)
	}

	err = dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query from other connection: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected other connection to see 0 rows before commit, got %d", count)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	err = dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Alice").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected other connection to see 1 row after commit, got %d", count)
	}
}

func TestTransactionRollback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite")

	config := sqlite.DefaultConfig()
	config.DatabasePath = dbPath
	config.Port = 13308
	config.Username = "testuser"
	config.Password = "testpass"

	proxy, err := sqlite.New(config)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	if err := proxy.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	time.Sleep(100 * time.Millisecond)

	dsn := fmt.Sprintf("testuser:testpass@tcp(127.0.0.1:%d)/?interpolateParams=true", config.Port)
	dbA, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer dbA.Close()

	dbB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer dbB.Close()

	for _, db := range []*sql.DB{dbA, dbB} {
		if err := db.Ping(); err != nil {
			t.Fatalf("Failed to ping: %v", err)
		}
	}

	_, err = dbA.Exec(`
		CREATE TABLE IF NOT EXISTS tx_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	tx, err := dbA.Begin()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	_, err = tx.Exec("INSERT INTO tx_users (name) VALUES (?)", "Bob")
	if err != nil {
		t.Fatalf("Failed to insert in transaction: %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("Failed to rollback transaction: %v", err)
	}

	var count int
	err = dbA.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Bob").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected rolled-back insert to be invisible, got %d rows", count)
	}

	err = dbB.QueryRow("SELECT COUNT(*) FROM tx_users WHERE name = ?", "Bob").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query from other connection: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected other connection to see 0 rows after rollback, got %d", count)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
