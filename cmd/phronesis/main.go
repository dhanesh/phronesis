package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhanesh/phronesis/internal/app"
)

// drainTimeout bounds the graceful shutdown window on SIGTERM / SIGINT.
// Tuned to fit inside typical k8s terminationGracePeriodSeconds (default 30s).
//
// Satisfies: O5, RT-12.1, INT-10
const drainTimeout = 30 * time.Second

func main() {
	cfg := app.LoadConfig()
	server, err := app.NewServer(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	// INT-10: SIGTERM/SIGINT cancels ctx, which triggers the server's graceful
	// shutdown: HTTP stops accepting new connections, in-flight requests drain,
	// then background goroutines (audit, snapshot, CRDT) close.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("phronesis listening on %s", cfg.Addr)
	if err := server.Serve(ctx, drainTimeout); err != nil {
		log.Fatalf("serve: %v", err)
	}
	log.Printf("phronesis stopped cleanly")
}
