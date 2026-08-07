// Command orphanarr finds completed downloads with no category, works out
// what media they contain, and files them where media servers will see them.
//
// This package is wiring only. Everything it does lives in internal/.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
)

// Set by the linker at release time; "dev" in a local build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("orphanarr %s (commit %s, built %s, %s)\n",
			version, commit, date, runtime.Version())
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("orphanarr starting", "version", version, "commit", commit)

	// Wiring lands here as the packages arrive: config, store, clients,
	// scanner, planner, executor, API. Until then the binary builds, reports
	// its version, and does nothing — which is the correct behaviour for a
	// program whose entire safety story is "never act without being told".
	log.Info("no subsystems wired yet; exiting cleanly")
}
