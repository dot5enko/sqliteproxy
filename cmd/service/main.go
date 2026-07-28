package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	sqlite "github.com/dot5enko/sqliteproxy"
)

func main() {
	config := sqlite.DefaultConfig()

	config.Address = envOr("SQLITE_MYSQL_ADDR", "0.0.0.0")
	config.Port = envIntOr("SQLITE_MYSQL_PORT", 3306)
	config.StorageRoot = envOr("SQLITE_STORAGE_ROOT", "/data")
	config.ManagementAddress = envOr("SQLITE_MGMT_ADDR", "0.0.0.0")
	config.ManagementPort = envIntOr("SQLITE_MGMT_PORT", 8080)
	config.WALMode = envBoolOr("SQLITE_WAL", true)
	config.MaxConns = envIntOr("SQLITE_MAX_CONNS", 10)
	config.BusyTimeout = time.Duration(envIntOr("SQLITE_BUSY_TIMEOUT_MS", 5000)) * time.Millisecond
	config.Debug = envBoolOr("SQLITE_DEBUG", false)

	proxy, err := sqlite.New(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := proxy.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	proxy.Stop()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
