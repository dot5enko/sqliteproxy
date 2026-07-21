package translator

import (
	"regexp"
	"strings"
)

// Translator converts MySQL SQL to SQLite SQL
type Translator struct {
	typeMap map[string]string
}

// New creates a new SQL translator
func New() *Translator {
	t := &Translator{
		typeMap: defaultTypeMap(),
	}
	return t
}

// Translate converts a MySQL SQL statement to SQLite
func (t *Translator) Translate(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return sql
	}

	// Normalize whitespace
	sql = normalizeWhitespace(sql)

	// Handle special commands
	if result, ok := t.handleSpecialCommands(sql); ok {
		return result
	}

	// Translate DDL
	sql = t.translateDDL(sql)

	// Translate DML
	sql = t.translateDML(sql)

	// Translate functions
	sql = t.translateFunctions(sql)

	// Clean up MySQL-specific syntax
	sql = t.cleanupMySQL(sql)

	return sql
}

// handleSpecialCommands handles MySQL meta-commands
func (t *Translator) handleSpecialCommands(sql string) (string, bool) {
	upper := strings.ToUpper(sql)

	// SHOW TABLES
	if upper == "SHOW TABLES" || upper == "SHOW FULL TABLES" {
		return "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'", true
	}

	// SHOW DATABASES / SHOW SCHEMAS are handled by the session handler
	// so each connection only sees its bound database.

	// SHOW CREATE TABLE
	if strings.HasPrefix(upper, "SHOW CREATE TABLE ") {
		table := strings.TrimSpace(sql[18:])
		return "SELECT sql FROM sqlite_master WHERE type='table' AND name='" + table + "'", true
	}

	// DESCRIBE table / DESC table
	if strings.HasPrefix(upper, "DESCRIBE ") || strings.HasPrefix(upper, "DESC ") {
		var table string
		if strings.HasPrefix(upper, "DESCRIBE ") {
			table = strings.TrimSpace(sql[9:])
		} else {
			table = strings.TrimSpace(sql[5:])
		}
		// Remove quotes if present
		table = strings.Trim(table, "`\"'")
		return "PRAGMA table_info('" + table + "')", true
	}

	// SHOW INDEX / SHOW KEYS
	if strings.HasPrefix(upper, "SHOW INDEX FROM ") || strings.HasPrefix(upper, "SHOW KEYS FROM ") {
		var table string
		if strings.HasPrefix(upper, "SHOW INDEX FROM ") {
			table = strings.TrimSpace(sql[16:])
		} else {
			table = strings.TrimSpace(sql[15:])
		}
		table = strings.Trim(table, "`\"'")
		return "PRAGMA index_list('" + table + "')", true
	}

	// SHOW COLUMNS
	if strings.HasPrefix(upper, "SHOW COLUMNS FROM ") {
		table := strings.TrimSpace(sql[18:])
		table = strings.Trim(table, "`\"'")
		return "PRAGMA table_info('" + table + "')", true
	}

	// SHOW WARNINGS
	if upper == "SHOW WARNINGS" {
		return "SELECT 1 LIMIT 0", true
	}

	// SHOW ERRORS
	if upper == "SHOW ERRORS" {
		return "SELECT 1 LIMIT 0", true
	}

	// USE database is handled by the session handler for binding enforcement.

	// SET statements (pass through or convert)
	if strings.HasPrefix(upper, "SET ") {
		return t.translateSet(sql), true
	}

	// Transaction commands
	if result, ok := t.translateTransaction(sql, upper); ok {
		return result, true
	}

	return "", false
}

// translateTransaction handles MySQL transaction commands
func (t *Translator) translateTransaction(sql, upper string) (string, bool) {
	// START TRANSACTION → BEGIN TRANSACTION
	if upper == "START TRANSACTION" || upper == "BEGIN" || upper == "BEGIN WORK" {
		return "BEGIN TRANSACTION", true
	}

	// COMMIT
	if upper == "COMMIT" || upper == "COMMIT WORK" {
		return "COMMIT", true
	}

	// ROLLBACK
	if upper == "ROLLBACK" || upper == "ROLLBACK WORK" {
		return "ROLLBACK", true
	}

	// SAVEPOINT name
	if strings.HasPrefix(upper, "SAVEPOINT ") {
		return sql, true // SQLite supports SAVEPOINT natively
	}

	// RELEASE SAVEPOINT name
	if strings.HasPrefix(upper, "RELEASE SAVEPOINT ") {
		return sql, true // SQLite supports RELEASE SAVEPOINT natively
	}

	// ROLLBACK TO SAVEPOINT name
	if strings.HasPrefix(upper, "ROLLBACK TO SAVEPOINT ") ||
		strings.HasPrefix(upper, "ROLLBACK WORK TO SAVEPOINT ") {
		return sql, true // SQLite supports ROLLBACK TO SAVEPOINT natively
	}

	// SET TRANSACTION ISOLATION LEVEL (no-op for SQLite)
	if strings.Contains(upper, "SET TRANSACTION") {
		return "SELECT 1", true
	}

	return "", false
}

// translateSet handles MySQL SET statements
func (t *Translator) translateSet(sql string) string {
	upper := strings.ToUpper(sql)

	// SET NAMES / SET CHARACTER SET (charset settings - no-op for SQLite)
	if strings.HasPrefix(upper, "SET NAMES") ||
		strings.HasPrefix(upper, "SET CHARACTER SET") ||
		strings.HasPrefix(upper, "SET CHARSET") {
		return "SELECT 1"
	}

	// SET SESSION / SET GLOBAL (ignore most session variables)
	if strings.HasPrefix(upper, "SET SESSION") || strings.HasPrefix(upper, "SET GLOBAL") {
		return "SELECT 1"
	}

	// SET autocommit, transaction isolation, etc (no-op)
	if strings.Contains(upper, "AUTOCOMMIT") ||
		strings.Contains(upper, "TRANSACTION ISOLATION") ||
		strings.Contains(upper, "SQL_MODE") ||
		strings.Contains(upper, "TIME_ZONE") ||
		strings.Contains(upper, "CHARACTER_SET") ||
		strings.Contains(upper, "COLLATION") {
		return "SELECT 1"
	}

	// Pass through other SET statements
	return sql
}

// translateDDL handles CREATE TABLE and other DDL
func (t *Translator) translateDDL(sql string) string {
	upper := strings.ToUpper(sql)

	if !strings.HasPrefix(upper, "CREATE ") &&
		!strings.HasPrefix(upper, "ALTER ") &&
		!strings.HasPrefix(upper, "DROP ") {
		return sql
	}

	// Handle CREATE TABLE
	if strings.HasPrefix(upper, "CREATE TABLE") || strings.HasPrefix(upper, "CREATE TEMPORARY TABLE") {
		return t.translateCreateTable(sql)
	}

	// Handle CREATE INDEX
	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		return t.translateCreateIndex(sql)
	}

	return sql
}

// translateCreateTable converts MySQL CREATE TABLE to SQLite
func (t *Translator) translateCreateTable(sql string) string {
	// Remove IF NOT EXISTS is fine in SQLite, keep it

	// Remove ENGINE=InnoDB, ENGINE=MyISAM, etc.
	sql = removeOption(sql, `ENGINE\s*=\s*\w+`)

	// Remove DEFAULT CHARSET=...
	sql = removeOption(sql, `DEFAULT\s+(CHARSET|CHARACTER\s+SET)\s*=\s*\w+`)

	// Remove COLLATE=...
	sql = removeOption(sql, `COLLATE\s*=\s*\w+`)

	// Remove AUTO_INCREMENT=...
	sql = removeOption(sql, `AUTO_INCREMENT\s*=\s*\d+`)

	// Remove ROW_FORMAT=...
	sql = removeOption(sql, `ROW_FORMAT\s*=\s*\w+`)

	// Remove COMMENT='...' from table level
	sql = removeOption(sql, `COMMENT\s*=\s*'[^']*'`)

	// Remove table options trailing semicolons before closing paren
	// This handles cases like ") ENGINE=InnoDB;"

	// Translate column types
	sql = t.translateColumnTypes(sql)

	// Translate AUTO_INCREMENT keyword
	sql = translateAutoIncrement(sql)

	return sql
}

// translateColumnTypes converts MySQL column types to SQLite
func (t *Translator) translateColumnTypes(sql string) string {
	// Replace MySQL types with SQLite equivalents
	for mysqlType, sqliteType := range t.typeMap {
		// Case-insensitive replacement for types
		re := regexp.MustCompile(`(?i)\b` + mysqlType + `\b`)
		sql = re.ReplaceAllString(sql, sqliteType)
	}

	return sql
}

// translateAutoIncrement handles AUTO_INCREMENT keyword
func translateAutoIncrement(sql string) string {
	// In SQLite, AUTOINCREMENT is only valid with INTEGER PRIMARY KEY
	// We need to ensure the column is INTEGER PRIMARY KEY

	// Remove standalone AUTO_INCREMENT keyword (it's not valid in SQLite)
	// But keep AUTOINCREMENT for INTEGER PRIMARY KEY columns
	re := regexp.MustCompile(`(?i)\s+AUTO_INCREMENT\b`)
	sql = re.ReplaceAllString(sql, "")

	return sql
}

// translateCreateIndex converts MySQL CREATE INDEX to SQLite
func (t *Translator) translateCreateIndex(sql string) string {
	// Remove IF NOT EXISTS (SQLite supports it, but let's be safe)
	// Remove USING BTREE/HASH (SQLite doesn't support)
	sql = removeOption(sql, `USING\s+(BTREE|HASH)`)
	return sql
}

// translateDML handles SELECT, INSERT, UPDATE, DELETE
func (t *Translator) translateDML(sql string) string {
	upper := strings.ToUpper(sql)

	// Handle SELECT
	if strings.HasPrefix(upper, "SELECT") {
		return t.translateSelect(sql)
	}

	// Handle INSERT
	if strings.HasPrefix(upper, "INSERT") {
		return t.translateInsert(sql)
	}

	// Handle UPDATE
	if strings.HasPrefix(upper, "UPDATE") {
		return t.translateUpdate(sql)
	}

	// Handle DELETE
	if strings.HasPrefix(upper, "DELETE") {
		return t.translateDelete(sql)
	}

	return sql
}

// translateSelect handles SELECT statement translation
func (t *Translator) translateSelect(sql string) string {
	// Convert LIMIT offset, count to LIMIT count OFFSET offset
	sql = translateLimitSyntax(sql)

	// Remove FORCE INDEX, USE INDEX, IGNORE INDEX
	sql = removeOption(sql, `(FORCE|USE|IGNORE)\s+INDEX\s*(\([^)]*\))?`)

	// Convert boolean literals
	sql = translateBoolean(sql)

	return sql
}

// translateInsert handles INSERT statement translation
func (t *Translator) translateInsert(sql string) string {
	// Remove ON DUPLICATE KEY UPDATE (not supported in SQLite)
	// This is a simplified approach - complex cases may need more work
	sql = removeOnDuplicateKeyUpdate(sql)

	return sql
}

// translateUpdate handles UPDATE statement translation
func (t *Translator) translateUpdate(sql string) string {
	// Remove LOW_PRIORITY, IGNORE keywords
	sql = removeOption(sql, `(?i)\bLOW_PRIORITY\b`)
	sql = removeOption(sql, `(?i)\bIGNORE\b`)

	// Remove FORCE INDEX
	sql = removeOption(sql, `(?i)FORCE\s+INDEX\s*(\([^)]*\))?`)

	return sql
}

// translateDelete handles DELETE statement translation
func (t *Translator) translateDelete(sql string) string {
	// Remove LOW_PRIORITY, QUICK, IGNORE keywords
	sql = removeOption(sql, `(?i)\bLOW_PRIORITY\b`)
	sql = removeOption(sql, `(?i)\bQUICK\b`)
	sql = removeOption(sql, `(?i)\bIGNORE\b`)

	// Convert DELETE FROM table LIMIT n (not supported in SQLite)
	// This would need subquery conversion, but for basic CRUD we skip

	return sql
}

// translateFunctions converts MySQL functions to SQLite equivalents
func (t *Translator) translateFunctions(sql string) string {
	// NOW() -> datetime('now')
	sql = replaceFunction(sql, `NOW\(\)`, `datetime('now')`)

	// CURDATE() -> date('now')
	sql = replaceFunction(sql, `CURDATE\(\)`, `date('now')`)

	// CURTIME() -> time('now')
	sql = replaceFunction(sql, `CURTIME\(\)`, `time('now')`)

	// CURRENT_TIMESTAMP -> CURRENT_TIMESTAMP (same in SQLite)

	// IFNULL -> IFNULL (same in SQLite)

	// GROUP_CONCAT -> GROUP_CONCAT (same in SQLite)

	// CONCAT(a, b) -> (a || b) - simplified, only handles 2 args
	sql = translateConcat(sql)

	// UNIX_TIMESTAMP() -> strftime('%s','now')
	sql = replaceFunction(sql, `UNIX_TIMESTAMP\(\)`, `strftime('%s','now')`)

	// FROM_UNIXTIME(n) -> datetime(n, 'unixepoch')
	sql = translateFromUnixtime(sql)

	// DATE_FORMAT -> strftime
	sql = translateDateFormat(sql)

	return sql
}

// translateConcat converts CONCAT(a, b) to (a || b)
func translateConcat(sql string) string {
	re := regexp.MustCompile(`(?i)CONCAT\s*\(([^,]+),\s*([^)]+)\)`)
	return re.ReplaceAllString(sql, `($1 || $2)`)
}

// translateFromUnixtime converts FROM_UNIXTIME(n) to datetime(n, 'unixepoch')
func translateFromUnixtime(sql string) string {
	re := regexp.MustCompile(`(?i)FROM_UNIXTIME\s*\(([^)]+)\)`)
	return re.ReplaceAllString(sql, `datetime($1, 'unixepoch')`)
}

// translateDateFormat converts DATE_FORMAT(date, format) to strftime
func translateDateFormat(sql string) string {
	// Convert MySQL format specifiers to SQLite
	re := regexp.MustCompile(`(?i)DATE_FORMAT\s*\(([^,]+),\s*'([^']+)'\)`)

	return re.ReplaceAllStringFunc(sql, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		date := parts[1]
		format := parts[2]

		// Convert MySQL format to SQLite strftime format
		sqliteFormat := convertDateFormat(format)

		return "strftime('" + sqliteFormat + "', " + date + ")"
	})
}

// convertDateFormat converts MySQL DATE_FORMAT specifiers to SQLite strftime
func convertDateFormat(format string) string {
	// MySQL: %Y-%m-%d %H:%i:%s
	// SQLite: %Y-%m-%d %H:%M:%S
	// Most are the same, but %i (minutes) -> %M in SQLite
	format = strings.ReplaceAll(format, "%i", "%M")
	return format
}

// translateLimitSyntax converts LIMIT offset, count to LIMIT count OFFSET offset
func translateLimitSyntax(sql string) string {
	// Match LIMIT offset, count pattern
	re := regexp.MustCompile(`(?i)LIMIT\s+(\d+)\s*,\s*(\d+)`)
	return re.ReplaceAllString(sql, "LIMIT $2 OFFSET $1")
}

// translateBoolean converts MySQL boolean literals
func translateBoolean(sql string) string {
	// TRUE -> 1, FALSE -> 0
	sql = regexp.MustCompile(`(?i)\bTRUE\b`).ReplaceAllString(sql, "1")
	sql = regexp.MustCompile(`(?i)\bFALSE\b`).ReplaceAllString(sql, "0")
	return sql
}

// removeOnDuplicateKeyUpdate removes ON DUPLICATE KEY UPDATE clause
func removeOnDuplicateKeyUpdate(sql string) string {
	re := regexp.MustCompile(`(?i)\s+ON\s+DUPLICATE\s+KEY\s+UPDATE\s+.*$`)
	return re.ReplaceAllString(sql, "")
}

// cleanupMySQL removes remaining MySQL-specific syntax
func (t *Translator) cleanupMySQL(sql string) string {
	// Remove backticks (SQLite uses double quotes for identifiers)
	sql = strings.ReplaceAll(sql, "`", "\"")

	// Remove MySQL-specific comments
	sql = removeOption(sql, `/\*![0-9]+\s+[^*]*\*/`)

	return sql
}

// Helper functions

func normalizeWhitespace(sql string) string {
	// Replace multiple spaces with single space
	re := regexp.MustCompile(`\s+`)
	sql = re.ReplaceAllString(sql, " ")
	return strings.TrimSpace(sql)
}

func removeOption(sql string, pattern string) string {
	re := regexp.MustCompile(`(?i)\s+` + pattern)
	return re.ReplaceAllString(sql, "")
}

func replaceFunction(sql string, pattern string, replacement string) string {
	re := regexp.MustCompile(`(?i)` + pattern)
	return re.ReplaceAllString(sql, replacement)
}

// defaultTypeMap returns MySQL to SQLite type mappings
func defaultTypeMap() map[string]string {
	return map[string]string{
		// Integer types
		"TINYINT":    "INTEGER",
		"SMALLINT":   "INTEGER",
		"MEDIUMINT":  "INTEGER",
		"INT":        "INTEGER",
		"INTEGER":    "INTEGER",
		"BIGINT":     "INTEGER",

		// Floating point
		"FLOAT":   "REAL",
		"DOUBLE":  "REAL",
		"DECIMAL": "REAL",
		"NUMERIC": "REAL",

		// String types
		"VARCHAR":    "TEXT",
		"CHAR":       "TEXT",
		"TINYTEXT":   "TEXT",
		"TEXT":       "TEXT",
		"MEDIUMTEXT": "TEXT",
		"LONGTEXT":   "TEXT",
		"ENUM":       "TEXT",
		"SET":        "TEXT",

		// Binary types
		"VARBINARY":  "BLOB",
		"BINARY":     "BLOB",
		"TINYBLOB":   "BLOB",
		"BLOB":       "BLOB",
		"MEDIUMBLOB": "BLOB",
		"LONGBLOB":   "BLOB",

		// Date/Time types
		"DATE":      "TEXT",
		"TIME":      "TEXT",
		"DATETIME":  "TEXT",
		"TIMESTAMP": "TEXT",
		"YEAR":      "INTEGER",

		// Boolean
		"BOOLEAN": "INTEGER",
		"BOOL":    "INTEGER",

		// JSON
		"JSON": "TEXT",
	}
}
