package server

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	"github.com/dot5enko/cloudfunctions/packages/sqlite/translator"
)

// SQLiteHandler implements the MySQL server handler interface
type SQLiteHandler struct {
	db         *sql.DB
	translator *translator.Translator
	user       string
	password   string
	sessions   *SessionManager
}

// NewSQLiteHandler creates a new SQLite handler
func NewSQLiteHandler(db *sql.DB, user, password string, sessions *SessionManager) *SQLiteHandler {
	return &SQLiteHandler{
		db:         db,
		translator: translator.New(),
		user:       user,
		password:   password,
		sessions:   sessions,
	}
}

// NewConnection is called when a new client connects
func (h *SQLiteHandler) NewConnection(c *server.Conn) {
	fmt.Printf("[INFO] New connection from %s\n", c.RemoteAddr())
}

// ConnectionClosed is called when a client disconnects
func (h *SQLiteHandler) ConnectionClosed(c *server.Conn) {
	fmt.Printf("[INFO] Connection closed: %s\n", c.RemoteAddr())
}

// getSessionFromConn extracts session from connection (stored via context)
func (h *SQLiteHandler) getSessionFromConn(c *server.Conn) *Session {
	// For now, we'll use a workaround - the session is stored in the handler
	// In a real implementation, we'd store it in the connection context
	return nil
}

// HandleQuery handles COM_QUERY commands (text protocol)
func (h *SQLiteHandler) HandleQuery(query string) (*mysql.Result, error) {
	fmt.Printf("[DEBUG] Query: %s\n", query)

	// Translate MySQL SQL to SQLite
	translated := h.translator.Translate(query)
	fmt.Printf("[DEBUG] Translated: %s\n", translated)

	// Check for transaction commands
	upper := strings.ToUpper(strings.TrimSpace(translated))

	// Handle BEGIN TRANSACTION
	if upper == "BEGIN TRANSACTION" {
		return h.handleBegin(query)
	}

	// Handle COMMIT
	if upper == "COMMIT" {
		return h.handleCommit(query)
	}

	// Handle ROLLBACK
	if upper == "ROLLBACK" {
		return h.handleRollback(query)
	}

	// Execute the translated query
	return h.execute(translated)
}

// handleBegin starts a transaction
func (h *SQLiteHandler) handleBegin(query string) (*mysql.Result, error) {
	// For now, we'll use a simple approach without per-session connections
	// The transaction will be handled at the connection level
	fmt.Printf("[DEBUG] Transaction started\n")
	return &mysql.Result{}, nil
}

// handleCommit commits the current transaction
func (h *SQLiteHandler) handleCommit(query string) (*mysql.Result, error) {
	fmt.Printf("[DEBUG] Transaction committed\n")
	return &mysql.Result{}, nil
}

// handleRollback rolls back the current transaction
func (h *SQLiteHandler) handleRollback(query string) (*mysql.Result, error) {
	fmt.Printf("[DEBUG] Transaction rolled back\n")
	return &mysql.Result{}, nil
}

// HandleStmtPrepare handles COM_STMT_PREPARE
func (h *SQLiteHandler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	fmt.Printf("[DEBUG] Prepare: %s\n", query)

	// Translate the query
	translated := h.translator.Translate(query)

	// Prepare the statement
	stmt, err := h.db.Prepare(translated)
	if err != nil {
		return 0, 0, nil, err
	}

	// Count parameters
	paramCount := strings.Count(translated, "?")

	// For SELECT queries, we need to return column count
	columnCount := 0
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC") ||
		strings.HasPrefix(upper, "PRAGMA") {
		rows, err := stmt.Query()
		if err == nil {
			cols, _ := rows.Columns()
			columnCount = len(cols)
			rows.Close()
		}
	}

	return paramCount, columnCount, stmt, nil
}

// HandleStmtExecute handles COM_STMT_EXECUTE
func (h *SQLiteHandler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*mysql.Result, error) {
	stmt, ok := context.(*sql.Stmt)
	if !ok {
		return nil, fmt.Errorf("invalid prepared statement context")
	}

	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC") ||
		strings.HasPrefix(upper, "PRAGMA") {
		rows, err := stmt.Query(args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		resultset, err := h.buildResultset(rows)
		if err != nil {
			return nil, err
		}

		return &mysql.Result{
			Resultset: resultset,
		}, nil
	}

	result, err := stmt.Exec(args...)
	if err != nil {
		return nil, err
	}

	affected, _ := result.RowsAffected()
	insertId, _ := result.LastInsertId()

	return &mysql.Result{
		AffectedRows: uint64(affected),
		InsertId:     uint64(insertId),
	}, nil
}

// HandleStmtClose handles COM_STMT_CLOSE
func (h *SQLiteHandler) HandleStmtClose(context interface{}) error {
	if stmt, ok := context.(*sql.Stmt); ok {
		return stmt.Close()
	}
	return nil
}

// HandleFieldList handles COM_FIELD_LIST command
func (h *SQLiteHandler) HandleFieldList(table string, fieldWildcard string) ([]*mysql.Field, error) {
	fmt.Printf("[DEBUG] FieldList: %s, %s\n", table, fieldWildcard)

	query := fmt.Sprintf("PRAGMA table_info('%s')", table)
	rows, err := h.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []*mysql.Field
	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull int
		var dfltValue interface{}
		var pk int

		if err := rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}

		field := &mysql.Field{
			Name: []byte(name),
			Type: mysql.MYSQL_TYPE_STRING,
		}

		fields = append(fields, field)
	}

	return fields, nil
}

// UseDB handles COM_INIT_DB command (database selection)
func (h *SQLiteHandler) UseDB(dbName string) error {
	fmt.Printf("[DEBUG] UseDB: %s\n", dbName)
	return nil
}

// HandleOtherCommand handles other MySQL commands
func (h *SQLiteHandler) HandleOtherCommand(cmd byte, data []byte) error {
	return mysql.NewError(mysql.ER_UNKNOWN_ERROR, fmt.Sprintf("command %d not supported", cmd))
}

// execute runs a SQL query and returns the result
func (h *SQLiteHandler) execute(query string) (*mysql.Result, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))

	if strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "PRAGMA") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC") ||
		strings.HasPrefix(upper, "EXPLAIN") {
		return h.query(query)
	}

	return h.exec(query)
}

// query handles SELECT-like statements that return rows
func (h *SQLiteHandler) query(query string) (*mysql.Result, error) {
	rows, err := h.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resultset, err := h.buildResultset(rows)
	if err != nil {
		return nil, err
	}

	return &mysql.Result{
		Resultset: resultset,
	}, nil
}

// exec handles statements that don't return rows
func (h *SQLiteHandler) exec(query string) (*mysql.Result, error) {
	result, err := h.db.Exec(query)
	if err != nil {
		return nil, err
	}

	affected, _ := result.RowsAffected()
	insertId, _ := result.LastInsertId()

	return &mysql.Result{
		AffectedRows: uint64(affected),
		InsertId:     uint64(insertId),
	}, nil
}

// buildResultset converts sql.Rows to mysql.Resultset
func (h *SQLiteHandler) buildResultset(rows *sql.Rows) (*mysql.Resultset, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var resultRows [][]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make([]interface{}, len(columns))
		for i, val := range values {
			if val == nil {
				row[i] = nil
			} else {
				switch v := val.(type) {
				case []byte:
					row[i] = string(v)
				case time.Time:
					row[i] = v.Format("2006-01-02 15:04:05")
				default:
					row[i] = fmt.Sprintf("%v", v)
				}
			}
		}

		resultRows = append(resultRows, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return mysql.BuildSimpleResultset(columns, resultRows, false)
}
