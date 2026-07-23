package server

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"
	"github.com/pingcap/tidb/pkg/parser/test_driver"
)

var sqlParser = parser.New()

// handleInfoSchemaQuery parses the query and handles information_schema requests.
// Returns (result, true) if handled, (nil, false) if the query is not an information_schema query.
func (h *SessionHandler) handleInfoSchemaQuery(query string) (*mysql.Result, bool) {
	stmts, _, err := sqlParser.ParseSQL(query)
	if err != nil || len(stmts) == 0 {
		return nil, false
	}

	sel, ok := stmts[0].(*ast.SelectStmt)
	if !ok {
		return nil, false
	}

	infoTable := extractInfoSchemaTable(sel)
	if infoTable == "" {
		return nil, false
	}

	conditions := extractWhereEqualities(sel.Where)

	switch infoTable {
	case "tables":
		result, err := h.queryInfoSchemaTables(conditions)
		if err != nil {
			fmt.Printf("[DEBUG] information_schema.tables error: %v\n", err)
			return &mysql.Result{}, true
		}
		return result, true
	case "columns":
		result, err := h.queryInfoSchemaColumns(conditions)
		if err != nil {
			fmt.Printf("[DEBUG] information_schema.columns error: %v\n", err)
			return &mysql.Result{}, true
		}
		return result, true
	case "schemata":
		result, err := h.queryInfoSchemaSchemata()
		if err != nil {
			fmt.Printf("[DEBUG] information_schema.schemata error: %v\n", err)
			return &mysql.Result{}, true
		}
		return result, true
	default:
		return &mysql.Result{}, true
	}
}

// extractInfoSchemaTable walks the FROM clause to find an information_schema table reference.
func extractInfoSchemaTable(stmt *ast.SelectStmt) string {
	if stmt.From == nil || stmt.From.TableRefs == nil {
		return ""
	}
	return findInfoSchemaInNode(stmt.From.TableRefs)
}

func findInfoSchemaInNode(node ast.ResultSetNode) string {
	switch n := node.(type) {
	case *ast.Join:
		if left := findInfoSchemaInNode(n.Left); left != "" {
			return left
		}
		if n.Right != nil {
			if right := findInfoSchemaInNode(n.Right); right != "" {
				return right
			}
		}
	case *ast.TableSource:
		return findInfoSchemaInNode(n.Source)
	case *ast.TableName:
		if strings.EqualFold(n.Schema.O, "information_schema") {
			return strings.ToLower(n.Name.O)
		}
	}
	return ""
}

// extractWhereEqualities walks a WHERE clause and extracts col = 'value' conditions.
func extractWhereEqualities(where ast.ExprNode) map[string]string {
	conditions := make(map[string]string)
	if where == nil {
		return conditions
	}
	collectEqualities(where, conditions)
	return conditions
}

func collectEqualities(expr ast.ExprNode, out map[string]string) {
	switch n := expr.(type) {
	case *ast.BinaryOperationExpr:
		if n.Op == opcode.EQ {
			if col, ok := n.L.(*ast.ColumnNameExpr); ok {
				if val, ok := n.R.(*test_driver.ValueExpr); ok {
					out[strings.ToLower(col.Name.Name.O)] = val.GetString()
					return
				}
			}
			if col, ok := n.R.(*ast.ColumnNameExpr); ok {
				if val, ok := n.L.(*test_driver.ValueExpr); ok {
					out[strings.ToLower(col.Name.Name.O)] = val.GetString()
					return
				}
			}
		}
		if n.Op == opcode.LogicAnd {
			collectEqualities(n.L, out)
			collectEqualities(n.R, out)
		}
	}
}

// queryInfoSchemaTables answers SELECT ... FROM information_schema.tables ...
func (h *SessionHandler) queryInfoSchemaTables(conditions map[string]string) (*mysql.Result, error) {
	db := h.db.Pool.DB()

	tableName := conditions["table_name"]
	tableSchema := conditions["table_schema"]

	if tableSchema != "" && tableSchema != h.db.Name {
		return &mysql.Result{}, nil
	}

	if tableName != "" {
		var count int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?",
			tableName,
		).Scan(&count)
		if err != nil {
			return nil, err
		}
		rs, err := mysql.BuildSimpleResultset([]string{"count(*)"}, [][]interface{}{
			{count},
		}, false)
		if err != nil {
			return nil, err
		}
		return &mysql.Result{Resultset: rs}, nil
	}

	rows, err := db.Query(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resultRows [][]interface{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		resultRows = append(resultRows, []interface{}{name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rs, err := mysql.BuildSimpleResultset([]string{"table_name"}, resultRows, false)
	if err != nil {
		return nil, err
	}
	return &mysql.Result{Resultset: rs}, nil
}

// queryInfoSchemaColumns answers SELECT ... FROM information_schema.columns ...
func (h *SessionHandler) queryInfoSchemaColumns(conditions map[string]string) (*mysql.Result, error) {
	tableName := conditions["table_name"]
	tableSchema := conditions["table_schema"]

	if tableSchema != "" && tableSchema != h.db.Name {
		return &mysql.Result{}, nil
	}

	if tableName == "" {
		return &mysql.Result{}, nil
	}

	db := h.db.Pool.DB()
	rows, err := db.Query("PRAGMA table_info('" + tableName + "')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resultRows [][]interface{}
	ordinalPos := 0
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		ordinalPos++

		isNullable := "YES"
		if notNull == 1 {
			isNullable = "NO"
		}

		columnKey := ""
		if pk == 1 {
			columnKey = "PRI"
		}

		var columnDefault interface{}
		if dfltValue.Valid {
			columnDefault = dfltValue.String
		}

		resultRows = append(resultRows, []interface{}{
			name,               // column_name
			columnDefault,      // column_default
			isNullable,         // is_nullable
			strings.ToUpper(ctype), // data_type
			nil,                // character_maximum_length
			strings.ToUpper(ctype), // column_type
			columnKey,          // column_key
			"",                 // extra
			"",                 // column_comment
			nil,                // numeric_precision
			nil,                // numeric_scale
			nil,                // datetime_precision
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rs, err := mysql.BuildSimpleResultset(
		[]string{
			"column_name", "column_default", "is_nullable", "data_type",
			"character_maximum_length", "column_type", "column_key", "extra",
			"column_comment", "numeric_precision", "numeric_scale", "datetime_precision",
		},
		resultRows, false,
	)
	if err != nil {
		return nil, err
	}
	return &mysql.Result{Resultset: rs}, nil
}

// queryInfoSchemaSchemata answers SELECT ... FROM information_schema.schemata ...
func (h *SessionHandler) queryInfoSchemaSchemata() (*mysql.Result, error) {
	rs, err := mysql.BuildSimpleResultset([]string{"schema_name"}, [][]interface{}{
		{h.db.Name},
	}, false)
	if err != nil {
		return nil, err
	}
	return &mysql.Result{Resultset: rs}, nil
}
