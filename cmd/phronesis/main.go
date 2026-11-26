package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/dhanesh/phronesis/internal/app"
	"github.com/dhanesh/phronesis/internal/redact"
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
	healthcheck := flag.String("healthcheck", "", "GET <url> (e.g. http://127.0.0.1:8080/api/readyz) and exit 0 on 200; for container health checks in images without curl")
	flag.Parse()

	if *healthcheck != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(*healthcheck)
		if err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck:", err)
			os.Exit(1)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "healthcheck: HTTP", resp.StatusCode)
			os.Exit(1)
		}
		return
	}

	if *showVersion {
		// Single-line format keeps scripting simple: `phronesis --version | awk ...`
		// Four fields — version (tag), commit (sha), buildTime (commit time),
		// goVersion (runtime-resolved) — map 1:1 to O3's contract.
		fmt.Printf("phronesis version=%s commit=%s buildTime=%s go=%s\n",
			version, commit, buildTime, runtime.Version())
		return
	}

	// Satisfies: RT-6 (BINDING) — bearer-token redaction at every egress.
	// Wrap the default slog handler so every logger downstream of
	// slog.Default() (i.e. the entire codebase) inherits redaction. Keeps
	// secrets out of logs even if a handler forgets to redact explicitly.
	slog.SetDefault(slog.New(redact.NewSlogHandler(slog.NewTextHandler(os.Stderr, nil))))

	cfg := app.LoadConfig()
	server, err := app.NewServer(cfg)
	if err != nil {
		slog.Error("create server", slog.String("err", err.Error()))
		os.Exit(1)
	}

	// INT-10: SIGTERM/SIGINT cancels ctx, which triggers the server's graceful
	// shutdown: HTTP stops accepting new connections, in-flight requests drain,
	// then background goroutines (audit, snapshot, CRDT) close.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("listening",
		slog.String("version", version),
		slog.String("addr", cfg.Addr),
	)
	if err := server.Serve(ctx, drainTimeout); err != nil {
		slog.Error("serve", slog.String("err", err.Error()))
		os.Exit(1)
	}
	slog.Info("stopped cleanly")
}
