//go:build linux

package main

import (
	"log"

	"github.com/777genius/agent-notifications/internal/daemon"
)

// runDaemon runs the notification daemon server on Linux
func runDaemon() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("[INFO] Starting notification daemon...")

	cfg := daemon.DefaultServerConfig()
	server, err := daemon.NewServer(cfg)
	if err != nil {
		log.Fatalf("[ERROR] Failed to create daemon server: %v", err)
	}

	if err := server.Run(); err != nil {
		log.Fatalf("[ERROR] Daemon server error: %v", err)
	}
}
