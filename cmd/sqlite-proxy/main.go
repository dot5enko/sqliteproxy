package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dot5enko/cloudfunctions/packages/sqlite"
)

func main() {
	// Parse command line flags
	var (
		address    = flag.String("address", "0.0.0.0", "Listen address")
		port       = flag.Int("port", 3306, "Listen port")
		dbPath     = flag.String("db", "./data.sqlite", "SQLite database path")
		username   = flag.String("user", "", "Username for authentication (empty = no auth)")
		password   = flag.String("password", "", "Password for authentication")
		walMode    = flag.Bool("wal", true, "Enable WAL mode")
		maxConns   = flag.Int("max-conns", 10, "Maximum database connections")
		debug      = flag.Bool("debug", false, "Enable debug logging")
		configFile = flag.String("config", "", "Path to configuration file (JSON)")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "SQLite Wire Proxy - MySQL-compatible SQLite server\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -db ./mydata.sqlite -port 3306\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -db ./mydata.sqlite -user admin -password secret\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -config sqlite-proxy.json\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nConnect with MySQL client:\n")
		fmt.Fprintf(os.Stderr, "  mysql -h 127.0.0.1 -P 3306 -u admin -psecret\n")
	}

	flag.Parse()

	// Load configuration
	var config sqlite.Config

	if *configFile != "" {
		// Load from config file
		fileConfig, err := sqlite.LoadConfig(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		config = fileConfig.ToConfig()

		// Override with command line flags if provided
		if isFlagPassed("address") {
			config.Address = *address
		}
		if isFlagPassed("port") {
			config.Port = *port
		}
		if isFlagPassed("db") {
			config.DatabasePath = *dbPath
		}
		if isFlagPassed("user") {
			config.Username = *username
		}
		if isFlagPassed("password") {
			config.Password = *password
		}
		if isFlagPassed("wal") {
			config.WALMode = *walMode
		}
		if isFlagPassed("max-conns") {
			config.MaxConns = *maxConns
		}
		if isFlagPassed("debug") {
			config.Debug = *debug
		}
	} else {
		// Use command line flags
		config = sqlite.DefaultConfig()
		config.Address = *address
		config.Port = *port
		config.DatabasePath = *dbPath
		config.Username = *username
		config.Password = *password
		config.WALMode = *walMode
		config.MaxConns = *maxConns
		config.Debug = *debug
	}

	// Create proxy
	proxy, err := sqlite.New(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Start proxy
	if err := proxy.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("[INFO] Press Ctrl+C to stop")

	<-sigChan
	fmt.Println("\n[INFO] Received shutdown signal")

	// Graceful shutdown
	if err := proxy.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
		os.Exit(1)
	}
}

// isFlagPassed checks if a flag was explicitly set
func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
