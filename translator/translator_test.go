package translator

import (
	"testing"
)

func TestTranslateBasicSelect(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple select",
			input:    "SELECT * FROM users",
			expected: "SELECT * FROM users",
		},
		{
			name:     "Select with where",
			input:    "SELECT * FROM users WHERE id = 1",
			expected: "SELECT * FROM users WHERE id = 1",
		},
		{
			name:     "Select with limit offset",
			input:    "SELECT * FROM users LIMIT 10, 20",
			expected: "SELECT * FROM users LIMIT 20 OFFSET 10",
		},
		{
			name:     "Select with backticks",
			input:    "SELECT `id`, `name` FROM `users`",
			expected: "SELECT \"id\", \"name\" FROM \"users\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateShowTables(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Show tables",
			input:    "SHOW TABLES",
			expected: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
		},
		{
			name:     "Show databases left for session handler",
			input:    "SHOW DATABASES",
			expected: "SHOW DATABASES",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateDescribe(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Describe table",
			input:    "DESCRIBE users",
			expected: "PRAGMA table_info('users')",
		},
		{
			name:     "Desc table",
			input:    "DESC users",
			expected: "PRAGMA table_info('users')",
		},
		{
			name:     "Describe quoted table",
			input:    "DESCRIBE `users`",
			expected: "PRAGMA table_info('users')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateCreateTable(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "Remove engine",
			input:    "CREATE TABLE users (id INT) ENGINE=InnoDB",
			contains: "CREATE TABLE users (id INTEGER)",
		},
		{
			name:     "Remove charset",
			input:    "CREATE TABLE users (id INT) DEFAULT CHARSET=utf8mb4",
			contains: "CREATE TABLE users (id INTEGER)",
		},
		{
			name:     "Translate types",
			input:    "CREATE TABLE users (id INT AUTO_INCREMENT, name VARCHAR(255), active BOOLEAN)",
			contains: "name TEXT",
		},
		{
			name:     "UNIQUE INDEX",
			input:    "CREATE TABLE `users` (`id` varchar(36),`email` varchar(255),`created_at` datetime(3) NULL,PRIMARY KEY (`id`),UNIQUE INDEX `idx_users_email` (`email`))",
			contains: `UNIQUE("email")`,
		},
		{
			name:     "Non-unique INDEX",
			input:    "CREATE TABLE `workspaces` (`id` varchar(36),`name` varchar(255) NOT NULL,`owner_id` varchar(36) NOT NULL,PRIMARY KEY (`id`),INDEX `idx_workspaces_owner_id` (`owner_id`))",
			contains: "CREATE INDEX IF NOT EXISTS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if !contains(result, tt.contains) {
				t.Errorf("Translate() = %q, should contain %q", result, tt.contains)
			}
		})
	}
}

func TestTranslateFunctions(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "NOW function",
			input:    "SELECT NOW()",
			expected: "SELECT datetime('now')",
		},
		{
			name:     "CURDATE function",
			input:    "SELECT CURDATE()",
			expected: "SELECT date('now')",
		},
		{
			name:     "UNIX_TIMESTAMP",
			input:    "SELECT UNIX_TIMESTAMP()",
			expected: "SELECT strftime('%s','now')",
		},
		{
			name:     "VERSION",
			input:    "SELECT VERSION()",
			expected: "SELECT sqlite_version()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateBoolean(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "TRUE literal",
			input:    "SELECT TRUE",
			expected: "SELECT 1",
		},
		{
			name:     "FALSE literal",
			input:    "SELECT FALSE",
			expected: "SELECT 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateSetStatements(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Set names",
			input:    "SET NAMES utf8mb4",
			expected: "SELECT 1",
		},
		{
			name:     "Set autocommit",
			input:    "SET autocommit=1",
			expected: "SELECT 1",
		},
		{
			name:     "Set session",
			input:    "SET SESSION sql_mode='STRICT_TRANS_TABLES'",
			expected: "SELECT 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTranslateInsert(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "Insert with on duplicate key",
			input:    "INSERT INTO users (id, name) VALUES (1, 'test') ON DUPLICATE KEY UPDATE name='test'",
			contains: "INSERT INTO users (id, name) VALUES (1, 'test')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if !contains(result, tt.contains) {
				t.Errorf("Translate() = %q, should contain %q", result, tt.contains)
			}
		})
	}
}

func TestTranslateUseStatement(t *testing.T) {
	tr := New()

	result := tr.Translate("USE mydb")
	if result != "USE mydb" {
		t.Errorf("Translate() = %q, want %q", result, "USE mydb")
	}
}

func TestTranslateTransactionCommands(t *testing.T) {
	tr := New()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "START TRANSACTION",
			input:    "START TRANSACTION",
			expected: "BEGIN TRANSACTION",
		},
		{
			name:     "BEGIN",
			input:    "BEGIN",
			expected: "BEGIN TRANSACTION",
		},
		{
			name:     "BEGIN WORK",
			input:    "BEGIN WORK",
			expected: "BEGIN TRANSACTION",
		},
		{
			name:     "COMMIT",
			input:    "COMMIT",
			expected: "COMMIT",
		},
		{
			name:     "COMMIT WORK",
			input:    "COMMIT WORK",
			expected: "COMMIT",
		},
		{
			name:     "ROLLBACK",
			input:    "ROLLBACK",
			expected: "ROLLBACK",
		},
		{
			name:     "ROLLBACK WORK",
			input:    "ROLLBACK WORK",
			expected: "ROLLBACK",
		},
		{
			name:     "SAVEPOINT",
			input:    "SAVEPOINT sp1",
			expected: "SAVEPOINT sp1",
		},
		{
			name:     "RELEASE SAVEPOINT",
			input:    "RELEASE SAVEPOINT sp1",
			expected: "RELEASE SAVEPOINT sp1",
		},
		{
			name:     "ROLLBACK TO SAVEPOINT",
			input:    "ROLLBACK TO SAVEPOINT sp1",
			expected: "ROLLBACK TO SAVEPOINT sp1",
		},
		{
			name:     "ROLLBACK WORK TO SAVEPOINT",
			input:    "ROLLBACK WORK TO SAVEPOINT sp1",
			expected: "ROLLBACK WORK TO SAVEPOINT sp1",
		},
		{
			name:     "SET TRANSACTION ISOLATION LEVEL",
			input:    "SET TRANSACTION ISOLATION LEVEL READ COMMITTED",
			expected: "SELECT 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tr.Translate(tt.input)
			if result != tt.expected {
				t.Errorf("Translate() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
