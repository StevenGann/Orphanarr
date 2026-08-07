// Command orphanarr finds completed downloads with no category, works out
// what media they contain, and files them where media servers will see them.
//
// This package is wiring only. Every decision lives in internal/.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/StevenGann/Orphanarr/internal/api"
	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/pipeline"
	"github.com/StevenGann/Orphanarr/internal/store"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configDir := flag.String("config", envOr("ORPHANARR__CONFIG_DIR", "/config"), "config directory")
	addr := flag.String("addr", envOr("ORPHANARR__ADDR", ":8790"), "listen address")
	flag.Parse()

	if *showVersion {
		fmt.Printf("orphanarr %s (commit %s, built %s, %s)\n", version, commit, date, runtime.Version())
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(*configDir, *addr, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configDir, addr string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("config dir: %w", err)
	}

	db, err := store.Open(ctx, filepath.Join(configDir, "orphanarr.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	cfg, err := config.Load(ctx, db)
	if err != nil {
		return err
	}
	cfg.ConfigDir = configDir
	if addr != "" {
		cfg.Addr = addr
	}

	// An API key is generated once and shown once. No default credentials,
	// ever — and no "no authentication" option that is not a deliberate
	// choice (DESIGN §8.3).
	if cfg.APIKey == "" {
		cfg.APIKey = api.NewAPIKey()
		if err := db.SetSetting(ctx, "api_key", cfg.APIKey); err != nil {
			return err
		}
		log.Warn("generated a new API key — this is shown once",
			"api_key", cfg.APIKey,
			"ui", fmt.Sprintf("http://localhost%s/?apikey=%s", cfg.Addr, cfg.APIKey))
	}

	// Startup validation is REPORTED, never fatal. A container that exits
	// on a config error is a container whose UI the user cannot open to
	// fix the config error.
	for _, p := range cfg.Validate() {
		log.Warn("configuration problem", "detail", p)
	}

	guard := fsx.NewGuarded(fsx.NewOS())
	guard.SetConfigRoot(configDir)
	guard.SetDryRun(cfg.DryRun)
	if cfg.DryRun {
		log.Info("dry run is ON — plans will be produced and zero files touched")
	}

	pl := pipeline.New(db, guard, cfg)

	// Clients and libraries are configured through the UI and loaded from
	// the database. With none configured the program serves its UI and
	// does nothing, which is the correct behaviour for a tool whose entire
	// safety story is "never act without being told".
	if err := db.LogEvent(ctx, store.Event{
		Code:    "STARTUP",
		Message: fmt.Sprintf("orphanarr %s started, dry_run=%v", version, cfg.DryRun),
	}); err != nil {
		log.Warn("could not write startup event", "err", err)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(db, cfg, pl, version).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// A periodic scan, and nothing more: scanning produces plans, and
	// plans are executed only on explicit approval.
	go func() {
		if cfg.ScanInterval <= 0 {
			return
		}
		t := time.NewTicker(cfg.ScanInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sum, err := pl.ScanNow(ctx)
				if err != nil {
					log.Warn("scan failed", "err", err)
					continue
				}
				log.Info("scan complete",
					"items", sum.Items, "orphans", sum.Orphans, "bytes", sum.Bytes)
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// SIGTERM is a first-class failure now that a step is a multi-gigabyte
	// copy rather than one link(2). Docker's default stop grace is about
	// ten seconds, so the shutdown contract has to be explicit: stop
	// accepting work, let the executor's context cancellation unwind the
	// copy loop, and leave Reconcile() to sweep whatever is left.
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
