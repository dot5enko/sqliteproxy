package sqlite

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// FileConfig represents the configuration file format
type FileConfig struct {
	Server     ServerConfig     `json:"server"`
	Storage    StorageConfig    `json:"storage"`
	Management ManagementConfig `json:"management"`
	Logging    LoggingConfig    `json:"logging"`
}

// ServerConfig holds MySQL wire-protocol server settings
type ServerConfig struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// StorageConfig holds multi-database storage settings
type StorageConfig struct {
	Root            string `json:"root"`
	WALMode         bool   `json:"wal_mode"`
	BusyTimeout     int    `json:"busy_timeout_ms"`
	MaxConnections  int    `json:"max_connections"`
}

// ManagementConfig holds HTTP management API settings
type ManagementConfig struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
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
		Storage: StorageConfig{
			Root:           "./storage",
			WALMode:        true,
			BusyTimeout:    5000,
			MaxConnections: 10,
		},
		Management: ManagementConfig{
			Address: "127.0.0.1",
			Port:    8080,
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

	return config, nil
}

// ToConfig converts FileConfig to Config
func (fc FileConfig) ToConfig() Config {
	return Config{
		Address:           fc.Server.Address,
		Port:              fc.Server.Port,
		StorageRoot:       fc.Storage.Root,
		WALMode:           fc.Storage.WALMode,
		BusyTimeout:       time.Duration(fc.Storage.BusyTimeout) * time.Millisecond,
		MaxConns:          fc.Storage.MaxConnections,
		ManagementAddress: fc.Management.Address,
		ManagementPort:    fc.Management.Port,
		Debug:             fc.Logging.Level == "debug",
	}
}
