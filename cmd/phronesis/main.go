package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/dhanesh/phronesis/internal/app"
)

// Build-time metadata. Each var is overridden via `-ldflags "-X main.<name>=..."`
// during `make release` (see .goreleaser.yaml). Defaults fire for `go run` and
// unflagged `go build`, which is how developers work day-to-day.
//
// `buildTime` is the git commit time (SOURCE_DATE_EPOCH), NOT wall-clock
// build time — this is what makes two builds of the same tag on different
// machines produce byte-identical binaries. See RT-2 and TN1.
//
// Satisfies: RT-2, O3.
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

// drainTimeout bounds the graceful shutdown window on SIGTERM / SIGINT.
// Tuned to fit inside typical k8s terminationGracePeriodSeconds (default 30s).
//
// Satisfies: O5, RT-12.1, INT-10
const drainTimeout = 30 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		// Single-line format keeps scripting simple: `phronesis --version | awk ...`
		// Four fields — version (tag), commit (sha), buildTime (commit time),
		// goVersion (runtime-resolved) — map 1:1 to O3's contract.
		fmt.Printf("phronesis version=%s commit=%s buildTime=%s go=%s\n",
			version, commit, buildTime, runtime.Version())
		return
	}

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

	log.Printf("phronesis %s listening on %s", version, cfg.Addr)
	if err := server.Serve(ctx, drainTimeout); err != nil {
		log.Fatalf("serve: %v", err)
	}
	log.Printf("phronesis stopped cleanly")
}
