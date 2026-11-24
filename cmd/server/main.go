package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"clauderelay/internal/config"
	"clauderelay/internal/relay"
)

var version = "dev"

func main() {
	configFlag := flag.String("config", "", "path to config.json")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if envVersion := os.Getenv("CLAUDE_RELAY_VERSION"); envVersion != "" {
		version = envVersion
	}
	if *showVersion {
		fmt.Println("ClaudeRelay", version)
		return
	}

	root, err := resolveRoot()
	if err != nil {
		fatal("resolve application directory: %v", err)
	}

	configPath := os.Getenv("CLAUDE_RELAY_CONFIG")
	if *configFlag != "" {
		configPath = *configFlag
	}
	if configPath == "" {
		configPath = filepath.Join(root, "config.json")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fatal("load config %s: %v", configPath, err)
	}

	handler, err := relay.New(cfg, version)
	if err != nil {
		fatal("initialize relay: %v", err)
	}

	listener, err := net.Listen("tcp", cfg.ListenAddress())
	if err != nil {
		fatal("listen %s: %v", cfg.ListenAddress(), err)
	}

	httpServer := &http.Server{
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 60 * time.Second,
		IdleTimeout:       65 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serveError := make(chan error, 1)
	go func() {
		serveError <- httpServer.Serve(listener)
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	select {
	case <-signalContext.Done():
	case err := <-serveError:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[ClaudeRelay] server error: %v\n", err)
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
}

func resolveRoot() (string, error) {
	if home := os.Getenv("CLAUDE_RELAY_HOME"); home != "" {
		return filepath.Abs(home)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(executable), nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[ClaudeRelay] "+format+"\n", args...)
	os.Exit(1)
}
