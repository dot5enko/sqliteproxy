package sqlite

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// FileConfig represents the configuration file format
type FileConfig struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Auth     AuthConfig     `json:"auth"`
	Logging  LoggingConfig  `json:"logging"`
}

// ServerConfig holds server settings
type ServerConfig struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// DatabaseConfig holds database settings
type DatabaseConfig struct {
	Path         string `json:"path"`
	WALMode      bool   `json:"wal_mode"`
	BusyTimeout  int    `json:"busy_timeout_ms"`
	MaxConns     int    `json:"max_connections"`
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	PasswordFile string `json:"password_file"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// DefaultFileConfig returns default configuration
func DefaultFileConfig() FileConfig {
	return FileConfig{
		Server: ServerConfig{
			Address: "0.0.0.0",
			Port:    3306,
		},
		Database: DatabaseConfig{
			Path:        "./data.sqlite",
			WALMode:     true,
			BusyTimeout: 5000,
			MaxConns:    10,
		},
		Auth: AuthConfig{
			Username: "",
			Password: "",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// LoadConfig loads configuration from a JSON file
func LoadConfig(path string) (FileConfig, error) {
	config := DefaultFileConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Handle password file
	if config.Auth.PasswordFile != "" && config.Auth.Password == "" {
		password, err := readPasswordFile(config.Auth.PasswordFile)
		if err != nil {
			return config, fmt.Errorf("failed to read password file: %w", err)
		}
		config.Auth.Password = password
	}

	return config, nil
}

// readPasswordFile reads a password from a file
func readPasswordFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// Trim whitespace and newlines
	password := string(data)
	for len(password) > 0 && (password[len(password)-1] == '\n' || password[len(password)-1] == '\r') {
		password = password[:len(password)-1]
	}
	return password, nil
}

// ToConfig converts FileConfig to Config
func (fc FileConfig) ToConfig() Config {
	return Config{
		Address:      fc.Server.Address,
		Port:         fc.Server.Port,
		DatabasePath: fc.Database.Path,
		WALMode:      fc.Database.WALMode,
		BusyTimeout:  time.Duration(fc.Database.BusyTimeout) * time.Millisecond,
		MaxConns:     fc.Database.MaxConns,
		Username:     fc.Auth.Username,
		Password:     fc.Auth.Password,
		Debug:        fc.Logging.Level == "debug",
	}
}
