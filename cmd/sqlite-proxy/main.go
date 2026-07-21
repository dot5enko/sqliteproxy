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
	var (
		address        = flag.String("address", "0.0.0.0", "MySQL wire-protocol listen address")
		port           = flag.Int("port", 3306, "MySQL wire-protocol listen port")
		storageRoot    = flag.String("storage", "./storage", "Storage root for managed databases")
		mgmtAddress    = flag.String("management-address", "127.0.0.1", "Management HTTP listen address")
		mgmtPort       = flag.Int("management-port", 8080, "Management HTTP listen port")
		walMode        = flag.Bool("wal", true, "Enable WAL mode for tenant databases")
		maxConns       = flag.Int("max-conns", 10, "Maximum connections per tenant database")
		debug          = flag.Bool("debug", false, "Enable debug logging")
		configFile     = flag.String("config", "", "Path to configuration file (JSON)")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "SQLite Wire Proxy - multi-tenant MySQL-compatible SQLite service\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -storage ./storage -port 3306\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -config sqlite-proxy.json\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nCreate a database:\n")
		fmt.Fprintf(os.Stderr, "  curl -sX POST http://127.0.0.1:8080/v1/databases -d '{\"label\":\"orders\"}'\n")
		fmt.Fprintf(os.Stderr, "\nConnect with MySQL client using generated credentials:\n")
		fmt.Fprintf(os.Stderr, "  mysql -h 127.0.0.1 -P 3306 -u <username> -p <database-name>\n")
	}

	flag.Parse()

	var config sqlite.Config

	if *configFile != "" {
		fileConfig, err := sqlite.LoadConfig(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		config = fileConfig.ToConfig()

		if isFlagPassed("address") {
			config.Address = *address
		}
		if isFlagPassed("port") {
			config.Port = *port
		}
		if isFlagPassed("storage") {
			config.StorageRoot = *storageRoot
		}
		if isFlagPassed("management-address") {
			config.ManagementAddress = *mgmtAddress
		}
		if isFlagPassed("management-port") {
			config.ManagementPort = *mgmtPort
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
		config = sqlite.DefaultConfig()
		config.Address = *address
		config.Port = *port
		config.StorageRoot = *storageRoot
		config.ManagementAddress = *mgmtAddress
		config.ManagementPort = *mgmtPort
		config.WALMode = *walMode
		config.MaxConns = *maxConns
		config.Debug = *debug
	}

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

	fmt.Println("[INFO] Press Ctrl+C to stop")
	<-sigChan
	fmt.Println("\n[INFO] Received shutdown signal")

	if err := proxy.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
		os.Exit(1)
	}
}

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
