package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"github.com/pingcap/tidb/pkg/parser/ast"

	storage "github.com/dot5enko/sqliteproxy/storage"
	translator "github.com/dot5enko/sqliteproxy/translator"
)

// HandlerFactory creates per-session MySQL protocol handlers.
type HandlerFactory struct {
	translator *translator.Translator
}

// NewHandlerFactory creates a factory for session-scoped handlers.
func NewHandlerFactory() *HandlerFactory {
	return &HandlerFactory{
		translator: translator.New(),
	}
}

// NewSessionHandler returns a handler bound to a client session and connection binding.
func (f *HandlerFactory) NewSessionHandler(session *Session, binding *ConnectionBinding) *SessionHandler {
	return &SessionHandler{
		translator: f.translator,
		session:    session,
		binding:    binding,
	}
}

// SessionHandler implements server.Handler for a single client connection.
type SessionHandler struct {
	translator *translator.Translator
	session    *Session
	binding    *ConnectionBinding
	db         *storage.Database
}

// Finalize binds the handler to the authenticated database after handshake.
func (h *SessionHandler) Finalize(username string) error {
	db, err := h.binding.Finalize(username)
	if err != nil {
		return err
	}
	if err := h.session.Bind(db); err != nil {
		return err
	}
	h.db = db
	return nil
}

// HandleQuery handles COM_QUERY commands (text protocol).
func (h *SessionHandler) HandleQuery(query string) (*mysql.Result, error) {
	fmt.Printf("[DEBUG] Query: %s\n", query)

	if h.db == nil {
		return nil, errNotBound()
	}

	stmts, _, err := sqlParser.ParseSQL(query)
	if err != nil || len(stmts) == 0 {
		return h.handleTranslated(query, stmts)
	}

	stmt := stmts[0]
	switch s := stmt.(type) {
	case *ast.UseStmt:
		name := strings.Trim(s.DBName, "`\"'")
		if err := h.selectDatabase(name); err != nil {
			return nil, err
		}
		return &mysql.Result{}, nil

	case *ast.ShowStmt:
		if s.Tp == ast.ShowDatabases {
			return h.showDatabases()
		}
		return h.handleTranslated(query, stmts)

	case *ast.SelectStmt:
		if isSelectDatabase(s) {
			return h.selectDatabaseName()
		}
		if extractInfoSchemaTable(s) != "" {
			if result, handled := h.handleInfoSchemaQuery(query); handled {
				return result, nil
			}
		}
		return h.handleTranslated(query, stmts)

	default:
		return h.handleTranslated(query, stmts)
	}
}

// handleTranslated translates the query and executes it.
func (h *SessionHandler) handleTranslated(query string, stmts []ast.StmtNode) (*mysql.Result, error) {
	if len(stmts) == 0 {
		translated := h.translator.Translate(query)
		fmt.Printf("[DEBUG] Translated: %s\n", translated)
		return h.executeTranslated(nil, translated)
	}

	var lastResult *mysql.Result
	for _, stmt := range stmts {
		translated := h.translator.Translate(stmt.Text())
		fmt.Printf("[DEBUG] Translated: %s\n", translated)
		result, err := h.executeTranslated(stmt, translated)
		if err != nil {
			return nil, err
		}
		lastResult = result
	}
	if lastResult != nil {
		return lastResult, nil
	}
	return &mysql.Result{}, nil
}

// executeTranslated executes a single translated statement, using the AST to determine the command type.
func (h *SessionHandler) executeTranslated(stmt ast.StmtNode, translated string) (*mysql.Result, error) {
	switch stmt.(type) {
	case *ast.BeginStmt:
		return h.handleBegin()
	case *ast.CommitStmt:
		return h.handleCommit()
	case *ast.RollbackStmt:
		return h.handleRollback()
	case *ast.SelectStmt, *ast.ShowStmt:
		return h.query(translated)
	case *ast.InsertStmt, *ast.UpdateStmt, *ast.DeleteStmt:
		return h.exec(translated)
	default:
		if isQueryType(translated) {
			return h.query(translated)
		}
		return h.exec(translated)
	}
}

// isQueryType checks if a translated statement returns rows.
func isQueryType(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "PRAGMA") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC") ||
		strings.HasPrefix(upper, "EXPLAIN")
}

// isQueryStmt checks if an AST statement returns rows.
func isQueryStmt(stmt ast.StmtNode) bool {
	switch stmt.(type) {
	case *ast.SelectStmt, *ast.ShowStmt:
		return true
	default:
		return isQueryType(stmt.Text())
	}
}

// isSelectDatabase checks if the SELECT statement is SELECT DATABASE().
func isSelectDatabase(sel *ast.SelectStmt) bool {
	if sel.Fields == nil {
		return false
	}
	for _, field := range sel.Fields.Fields {
		if fn, ok := field.Expr.(*ast.FuncCallExpr); ok {
			if strings.EqualFold(fn.FnName.L, "database") {
				return true
			}
		}
	}
	return false
}

func (h *SessionHandler) showDatabases() (*mysql.Result, error) {
	rs, err := mysql.BuildSimpleResultset([]string{"Database"}, [][]interface{}{
		{h.db.Name},
	}, false)
	if err != nil {
		return nil, err
	}
	return &mysql.Result{Resultset: rs}, nil
}

func (h *SessionHandler) selectDatabaseName() (*mysql.Result, error) {
	rs, err := mysql.BuildSimpleResultset([]string{"DATABASE()"}, [][]interface{}{
		{h.db.Name},
	}, false)
	if err != nil {
		return nil, err
	}
	return &mysql.Result{Resultset: rs}, nil
}

func (h *SessionHandler) selectDatabase(name string) error {
	if h.db == nil {
		// Pre-auth handshake path
		return h.binding.SetRequestedDB(name)
	}
	if name != h.db.Name {
		return errDBAccessDenied(name)
	}
	h.session.SetDatabase(name)
	return nil
}

func (h *SessionHandler) handleBegin() (*mysql.Result, error) {
	if h.session.IsInTransaction() {
		return nil, fmt.Errorf("transaction already in progress")
	}

	if err := h.ensureConnection(); err != nil {
		return nil, err
	}

	tx, err := h.session.GetConnection().BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	h.session.BeginTransaction(tx)
	fmt.Printf("[DEBUG] Transaction started (session: %s)\n", h.session.ID)
	return &mysql.Result{}, nil
}

func (h *SessionHandler) handleCommit() (*mysql.Result, error) {
	if err := h.session.CommitTransaction(); err != nil {
		return nil, err
	}

	fmt.Printf("[DEBUG] Transaction committed (session: %s)\n", h.session.ID)
	return &mysql.Result{}, nil
}

func (h *SessionHandler) handleRollback() (*mysql.Result, error) {
	if !h.session.IsInTransaction() {
		return &mysql.Result{}, nil
	}

	if err := h.session.RollbackTransaction(); err != nil {
		return nil, err
	}

	fmt.Printf("[DEBUG] Transaction rolled back (session: %s)\n", h.session.ID)
	return &mysql.Result{}, nil
}

// HandleStmtPrepare handles COM_STMT_PREPARE.
func (h *SessionHandler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	fmt.Printf("[DEBUG] Prepare: %s\n", query)

	if h.db == nil {
		return 0, 0, nil, errNotBound()
	}

	translated := h.translator.Translate(query)

	if err := h.ensureConnection(); err != nil {
		return 0, 0, nil, err
	}

	stmt, err := h.prepare(translated)
	if err != nil {
		return 0, 0, nil, err
	}

	paramCount := strings.Count(translated, "?")

	columnCount := 0
	stmts, _, _ := sqlParser.ParseSQL(query)
	if len(stmts) > 0 && isQueryStmt(stmts[0]) {
		rows, err := stmt.Query()
		if err == nil {
			cols, _ := rows.Columns()
			columnCount = len(cols)
			rows.Close()
		}
	}

	return paramCount, columnCount, stmt, nil
}

// HandleStmtExecute handles COM_STMT_EXECUTE.
func (h *SessionHandler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*mysql.Result, error) {
	stmt, ok := context.(*sql.Stmt)
	if !ok {
		return nil, fmt.Errorf("invalid prepared statement context")
	}

	stmts, _, _ := sqlParser.ParseSQL(query)
	if len(stmts) > 0 && isQueryStmt(stmts[0]) {
		rows, err := stmt.Query(args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		resultset, err := buildResultset(rows)
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

// HandleStmtClose handles COM_STMT_CLOSE.
func (h *SessionHandler) HandleStmtClose(context interface{}) error {
	if stmt, ok := context.(*sql.Stmt); ok {
		return stmt.Close()
	}
	return nil
}

// HandleFieldList handles COM_FIELD_LIST command.
func (h *SessionHandler) HandleFieldList(table string, fieldWildcard string) ([]*mysql.Field, error) {
	fmt.Printf("[DEBUG] FieldList: %s, %s\n", table, fieldWildcard)

	if h.db == nil {
		return nil, errNotBound()
	}

	query := fmt.Sprintf("PRAGMA table_info('%s')", strings.ReplaceAll(table, "'", "''"))
	rows, err := h.queryRows(query)
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

		fields = append(fields, &mysql.Field{
			Name: []byte(name),
			Type: mysql.MYSQL_TYPE_STRING,
		})
	}

	return fields, nil
}

// UseDB handles COM_INIT_DB command (database selection).
func (h *SessionHandler) UseDB(dbName string) error {
	fmt.Printf("[DEBUG] UseDB: %s\n", dbName)
	return h.selectDatabase(dbName)
}

// HandleOtherCommand handles other MySQL commands.
func (h *SessionHandler) HandleOtherCommand(cmd byte, data []byte) error {
	return mysql.NewError(mysql.ER_UNKNOWN_ERROR, fmt.Sprintf("command %d not supported", cmd))
}

func (h *SessionHandler) ensureConnection() error {
	if h.session.GetConnection() != nil {
		return nil
	}
	if h.db == nil {
		return errNotBound()
	}

	conn, err := h.db.Pool.DB().Conn(context.Background())
	if err != nil {
		return fmt.Errorf("failed to acquire session connection: %w", err)
	}

	h.session.SetConnection(conn)
	return nil
}

func (h *SessionHandler) prepare(query string) (*sql.Stmt, error) {
	if tx := h.session.GetTransaction(); tx != nil {
		return tx.Prepare(query)
	}

	if err := h.ensureConnection(); err != nil {
		return nil, err
	}

	return h.session.GetConnection().PrepareContext(context.Background(), query)
}

func (h *SessionHandler) query(query string) (*mysql.Result, error) {
	rows, err := h.queryRows(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resultset, err := buildResultset(rows)
	if err != nil {
		return nil, err
	}

	rowsCount := 0
	if resultset != nil && resultset.RowDatas != nil {
		rowsCount = len(resultset.RowDatas)
	}
	fmt.Printf("[DEBUG] Result: rows_count=%d\n", rowsCount)

	if resultset != nil && resultset.RowDatas != nil {
		for i, row := range resultset.RowDatas {
			if i >= 2 {
				break
			}
			fieldValues, err := row.ParseText(resultset.Fields, nil)
			if err != nil {
				fmt.Printf("[DEBUG]   row[%d]: error parsing: %v\n", i, err)
				continue
			}
			vals := make([]interface{}, len(fieldValues))
			for j, fv := range fieldValues {
				vals[j] = fv.Value()
			}
			fmt.Printf("[DEBUG]   row[%d]: %v\n", i, vals)
		}
	}

	return &mysql.Result{
		Resultset: resultset,
	}, nil
}

func (h *SessionHandler) exec(query string) (*mysql.Result, error) {
	result, err := h.execResult(query)
	if err != nil {
		return nil, err
	}

	affected, _ := result.RowsAffected()
	insertId, _ := result.LastInsertId()

	fmt.Printf("[DEBUG] Result: affected=%d, insert_id=%d\n", affected, insertId)

	return &mysql.Result{
		AffectedRows: uint64(affected),
		InsertId:     uint64(insertId),
	}, nil
}

func (h *SessionHandler) queryRows(query string) (*sql.Rows, error) {
	if err := h.ensureConnection(); err != nil {
		return nil, err
	}

	ctx := context.Background()
	if tx := h.session.GetTransaction(); tx != nil {
		return tx.QueryContext(ctx, query)
	}

	return h.session.GetConnection().QueryContext(ctx, query)
}

func (h *SessionHandler) execResult(query string) (sql.Result, error) {
	if err := h.ensureConnection(); err != nil {
		return nil, err
	}

	ctx := context.Background()
	if tx := h.session.GetTransaction(); tx != nil {
		return tx.ExecContext(ctx, query)
	}

	return h.session.GetConnection().ExecContext(ctx, query)
}

func buildResultset(rows *sql.Rows) (*mysql.Resultset, error) {
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

// Ensure SessionHandler implements server.Handler.
var _ server.Handler = (*SessionHandler)(nil)
