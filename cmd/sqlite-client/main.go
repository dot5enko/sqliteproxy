package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/peterh/liner"
)

var version = "1.0.0"

var historyFile string

func init() {
	home, err := os.UserHomeDir()
	if err == nil {
		historyFile = filepath.Join(home, ".sqlite_history")
	}
}

func main() {
	var (
		host     = "127.0.0.1"
		port     = 3306
		user     = "root"
		password = ""
		database = ""
		debug    = false
	)

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--host":
			if i+1 < len(args) {
				host = args[i+1]
				i++
			}
		case "-P", "--port":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &port)
				i++
			}
		case "-u", "--user":
			if i+1 < len(args) {
				user = args[i+1]
				i++
			}
		case "-p", "--password":
			if i+1 < len(args) {
				password = args[i+1]
				i++
			}
		case "--debug":
			debug = true
		case "-V", "--version":
			fmt.Printf("sqlite-client %s\n", version)
			os.Exit(0)
		case "--help":
			printUsage()
			os.Exit(0)
		default:
			if database == "" && !strings.HasPrefix(args[i], "-") {
				database = args[i]
			}
		}
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?interpolateParams=true", user, password, host, port, database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Connected to %s:%d as %s\n", host, port, user)
	fmt.Printf("Type 'help' for commands, 'quit' to exit.\n\n")

	line := liner.NewLiner()
	defer line.Close()

	line.SetCtrlCAborts(true)

	// Load history
	if historyFile != "" {
		if f, err := os.Open(historyFile); err == nil {
			line.ReadHistory(f)
			f.Close()
		}
	}

	// Set tab completion
	line.SetCompleter(func(line string) (c []string) {
		lower := strings.ToLower(strings.TrimSpace(line))
		commands := []string{
			"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER",
			"SHOW TABLES", "DESCRIBE", "FROM", "WHERE", "AND", "OR", "ORDER BY",
			"GROUP BY", "HAVING", "LIMIT", "OFFSET", "JOIN", "LEFT JOIN",
			"RIGHT JOIN", "INNER JOIN", "ON", "AS", "SET", "VALUES", "INTO",
			"BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT",
			"help", "quit", "exit", "status", "version",
		}
		for _, cmd := range commands {
			if strings.HasPrefix(strings.ToUpper(cmd), strings.ToUpper(lower)) {
				c = append(c, cmd)
			}
		}
		return
	})

	// Save history on exit
	defer func() {
		if historyFile != "" {
			if f, err := os.Create(historyFile); err == nil {
				line.WriteHistory(f)
				f.Close()
			}
		}
	}()

	multilineBuffer := ""

	for {
		prompt := "sqlite> "
		if multilineBuffer != "" {
			prompt = "    -> "
		}

		input, err := line.Prompt(prompt)
		if err != nil {
			if err == liner.ErrPromptAborted {
				fmt.Println()
				multilineBuffer = ""
				continue
			}
			break
		}

		input = strings.TrimSpace(input)

		// Handle empty input
		if input == "" {
			continue
		}

		// Handle multiline continuation
		if strings.HasSuffix(input, "\\") {
			input = strings.TrimSuffix(input, "\\")
			multilineBuffer += input + " "
			continue
		}

		// Combine multiline buffer
		if multilineBuffer != "" {
			input = multilineBuffer + input
			multilineBuffer = ""
		}

		// Add to history
		line.AppendHistory(input)

		lower := strings.ToLower(input)

		switch {
		case lower == "quit" || lower == "exit" || lower == "\\q":
			fmt.Println("Bye!")
			return

		case lower == "help" || lower == "\\h":
			printHelp()

		case lower == "version" || lower == "\\v":
			fmt.Printf("sqlite-client %s\n", version)

		case lower == "status" || lower == "\\s":
			printStatus(db)

		case strings.HasPrefix(lower, "use "):
			database = strings.TrimSpace(input[4:])
			fmt.Printf("Database changed to '%s'\n", database)

		case lower == "show tables" || lower == "\\t":
			executeQuery(db, "SHOW TABLES", debug)

		case strings.HasPrefix(lower, "describe ") || strings.HasPrefix(lower, "desc "):
			var table string
			if strings.HasPrefix(lower, "describe ") {
				table = strings.TrimSpace(input[9:])
			} else {
				table = strings.TrimSpace(input[5:])
			}
			table = strings.Trim(table, "`\"'")
			executeQuery(db, "DESCRIBE "+table, debug)

		default:
			executeQuery(db, input, debug)
		}
	}
}

func printUsage() {
	fmt.Println("Usage: sqlite-client [options] [database]")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  -h, --host HOST      Server host (default: 127.0.0.1)")
	fmt.Println("  -P, --port PORT      Server port (default: 3306)")
	fmt.Println("  -u, --user USER      Username (default: root)")
	fmt.Println("  -p, --password PASS  Password")
	fmt.Println("  --debug              Show query execution time")
	fmt.Println("  -V, --version        Show version")
	fmt.Println("  --help               Show this help")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  sqlite-client -h 127.0.0.1 -P 3306 -u admin -p secret mydb")
	fmt.Println("  sqlite-client --debug -u admin mydb")
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  help, \\h        Show this help")
	fmt.Println("  quit, exit, \\q  Exit the client")
	fmt.Println("  version, \\v     Show version")
	fmt.Println("  status, \\s      Show connection status")
	fmt.Println("  show tables, \\t List all tables")
	fmt.Println("  DESCRIBE table   Show table structure")
	fmt.Println("  USE database     Change current database")
	fmt.Println("")
	fmt.Println("Features:")
	fmt.Println("  Arrow keys       Edit current line (left/right)")
	fmt.Println("  Up/Down arrows   Navigate command history")
	fmt.Println("  Tab              Auto-complete SQL keywords")
	fmt.Println("  Ctrl+C           Cancel current input")
	fmt.Println("  \\ at end of line Continue on next line")
	fmt.Println("")
	fmt.Println("History is saved to ~/.sqlite_history")
}

func printStatus(db *sql.DB) {
	stats := db.Stats()
	fmt.Printf("Connection:     OK\n")
	fmt.Printf("Open conns:     %d\n", stats.OpenConnections)
	fmt.Printf("In use:         %d\n", stats.InUse)
	fmt.Printf("Idle:           %d\n", stats.Idle)
	if historyFile != "" {
		fmt.Printf("History file:   %s\n", historyFile)
	}
}

func executeQuery(db *sql.DB, query string, debug bool) {
	start := time.Now()

	upper := strings.ToUpper(strings.TrimSpace(query))
	isQuery := strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC") ||
		strings.HasPrefix(upper, "PRAGMA") ||
		strings.HasPrefix(upper, "EXPLAIN")

	if isQuery {
		executeSelect(db, query, start, debug)
	} else {
		executeExec(db, query, start, debug)
	}
}

func executeSelect(db *sql.DB, query string, start time.Time, debug bool) {
	rows, err := db.Query(query)
	if err != nil {
		elapsed := time.Since(start)
		fmt.Printf("ERROR: %v\n", err)
		if debug {
			fmt.Printf("(%s)\n", elapsed.Round(time.Microsecond))
		}
		return
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}

	colCount := len(columns)

	var allRows [][]string
	for rows.Next() {
		values := make([]interface{}, colCount)
		ptrs := make([]interface{}, colCount)
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			fmt.Printf("ERROR: %v\n", err)
			return
		}

		row := make([]string, colCount)
		for i, val := range values {
			if val == nil {
				row[i] = "NULL"
			} else {
				switch v := val.(type) {
				case []byte:
					row[i] = string(v)
				default:
					row[i] = fmt.Sprintf("%v", v)
				}
			}
		}
		allRows = append(allRows, row)
	}

	if err := rows.Err(); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}

	elapsed := time.Since(start)

	if colCount == 0 {
		fmt.Println("Empty set")
		if debug {
			fmt.Printf("(%s)\n", elapsed.Round(time.Microsecond))
		}
		return
	}

	widths := make([]int, colCount)
	for i, col := range columns {
		widths[i] = len(col)
	}
	for _, row := range allRows {
		for i, val := range row {
			if len(val) > widths[i] {
				widths[i] = len(val)
			}
		}
	}

	printSeparator(widths)
	printRow(columns, widths)
	printSeparator(widths)

	for _, row := range allRows {
		printRow(row, widths)
	}

	printSeparator(widths)

	rowCount := len(allRows)
	rowWord := "rows"
	if rowCount == 1 {
		rowWord = "row"
	}
	fmt.Printf("%d %s in set", rowCount, rowWord)
	if debug {
		fmt.Printf(" (%s)", elapsed.Round(time.Microsecond))
	}
	fmt.Println()
}

func executeExec(db *sql.DB, query string, start time.Time, debug bool) {
	result, err := db.Exec(query)
	if err != nil {
		elapsed := time.Since(start)
		fmt.Printf("ERROR: %v\n", err)
		if debug {
			fmt.Printf("(%s)\n", elapsed.Round(time.Microsecond))
		}
		return
	}

	elapsed := time.Since(start)

	affected, _ := result.RowsAffected()

	upper := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(upper, "INSERT"):
		fmt.Printf("Query OK, %d row affected", affected)
	case strings.HasPrefix(upper, "UPDATE"):
		fmt.Printf("Query OK, %d row(s) affected", affected)
	case strings.HasPrefix(upper, "DELETE"):
		fmt.Printf("Query OK, %d row(s) affected", affected)
	default:
		fmt.Printf("Query OK")
	}

	if debug {
		fmt.Printf(" (%s)", elapsed.Round(time.Microsecond))
	}
	fmt.Println()
}

func printRow(cols []string, widths []int) {
	fmt.Print("|")
	for i, col := range cols {
		fmt.Printf(" %-*s |", widths[i], col)
	}
	fmt.Println()
}

func printSeparator(widths []int) {
	fmt.Print("+")
	for _, w := range widths {
		fmt.Print(strings.Repeat("-", w+2) + "+")
	}
	fmt.Println()
}
