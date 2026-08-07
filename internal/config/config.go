// Package config resolves settings from environment and database.
//
// One source of truth per setting: the database is authoritative and the UI
// is its editor, but an environment override at boot wins and renders that
// key read-only in the UI (DESIGN §8.1). A setting that two places can edit
// is a setting that will disagree with itself.
package config

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/StevenGann/Orphanarr/internal/store"
)

const envPrefix = "ORPHANARR__"

// Config is the resolved runtime configuration.
type Config struct {
	ConfigDir string
	Addr      string
	APIKey    string

	// DryRun ships ON and stays on until the user turns it off
	// deliberately. It is the difference between a tool that can be
	// evaluated and a tool that must be trusted.
	DryRun   bool
	AutoFile bool

	Mode            string // copy | link_or_copy | link
	Collision       string // skip | suffix | fail
	ClientWrite     string // none | tag
	VerifyCopies    bool
	MaxPlansPerRun  int
	ReserveBytes    uint64
	ReserveFraction float64
	FileMode        uint32
	DirMode         uint32

	ScanInterval  time.Duration
	SettleWindow  time.Duration
	AutoThreshold float64
	ReviewThresh  float64
	Ambiguity     float64
	PDFDefault    string

	// ReadOnly lists keys pinned by the environment, so the UI can show
	// them as uneditable rather than silently discarding edits.
	ReadOnly map[string]bool
}

// Defaults are DESIGN §8.2's shipped values.
//
// Two are worth noting. Mode defaults to copy after the 2026-08-07
// amendment, auto-upgrading per pair as the probe passes. And the reserve
// is max(absolute, fraction) rather than a flat 1 GiB, because the harm is
// not Orphanarr stopping — it is Orphanarr filling the array so
// qBittorrent, Plex and the next download fail with no visible connection
// to us.
func Defaults() Config {
	return Config{
		ConfigDir:       "/config",
		Addr:            ":8790",
		DryRun:          true,
		AutoFile:        false,
		Mode:            "copy",
		Collision:       "skip",
		ClientWrite:     "tag",
		VerifyCopies:    false,
		MaxPlansPerRun:  25,
		ReserveBytes:    10 << 30,
		ReserveFraction: 0.05,
		FileMode:        0o644,
		DirMode:         0o755,
		ScanInterval:    15 * time.Minute,
		SettleWindow:    5 * time.Minute,
		AutoThreshold:   0.85,
		ReviewThresh:    0.50,
		Ambiguity:       0.10,
		PDFDefault:      "review",
		ReadOnly:        map[string]bool{},
	}
}

// Load resolves defaults, then database values, then environment overrides.
func Load(ctx context.Context, db *store.DB) (Config, error) {
	c := Defaults()

	stored, err := db.AllSettings(ctx)
	if err != nil {
		return c, err
	}
	apply(&c, func(key string) (string, bool) {
		v, ok := stored[key]
		return v, ok
	}, nil)

	// Environment last, and it marks each key read-only. A UI that lets a
	// user edit a value the environment will overwrite at next boot is a
	// UI that lies.
	apply(&c, func(key string) (string, bool) {
		v, ok := os.LookupEnv(envPrefix + strings.ToUpper(key))
		return v, ok
	}, c.ReadOnly)

	if c.APIKey == "" {
		c.APIKey = stored["api_key"]
	}
	return c, nil
}

type lookup func(key string) (string, bool)

func apply(c *Config, get lookup, ro map[string]bool) {
	str := func(key string, dst *string) {
		if v, ok := get(key); ok {
			*dst = v
			mark(ro, key)
		}
	}
	boolean := func(key string, dst *bool) {
		v, ok := get(key)
		if !ok {
			return
		}
		mark(ro, key)
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			*dst = true
		case "false", "0", "no", "off":
			*dst = false
		default:
			// An unrecognised value leaves the DEFAULT in place rather
			// than resolving to false. The old parser resolved anything
			// it did not recognise to false, so ORPHANARR__OPS__DRY_RUN=True
			// silently DISABLED dry-run — a safety default that failed
			// open on a capitalisation.
		}
	}
	integer := func(key string, dst *int) {
		if v, ok := get(key); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
				mark(ro, key)
			}
		}
	}
	u64 := func(key string, dst *uint64) {
		if v, ok := get(key); ok {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				*dst = n
				mark(ro, key)
			}
		}
	}
	f64 := func(key string, dst *float64) {
		if v, ok := get(key); ok {
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				*dst = n
				mark(ro, key)
			}
		}
	}
	dur := func(key string, dst *time.Duration) {
		if v, ok := get(key); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = time.Duration(n) * time.Second
				mark(ro, key)
			}
		}
	}
	mode := func(key string, dst *uint32) {
		if v, ok := get(key); ok {
			if n, err := strconv.ParseUint(v, 8, 32); err == nil {
				*dst = uint32(n)
				mark(ro, key)
			}
		}
	}

	str("config_dir", &c.ConfigDir)
	str("addr", &c.Addr)
	str("api_key", &c.APIKey)
	boolean("ops__dry_run", &c.DryRun)
	boolean("ops__auto_file", &c.AutoFile)
	str("ops__mode", &c.Mode)
	str("ops__collision", &c.Collision)
	str("ops__client_write", &c.ClientWrite)
	boolean("ops__verify_copies", &c.VerifyCopies)
	integer("ops__max_plans_per_run", &c.MaxPlansPerRun)
	u64("ops__reserve_bytes", &c.ReserveBytes)
	f64("ops__reserve_fraction", &c.ReserveFraction)
	mode("ops__file_mode", &c.FileMode)
	mode("ops__dir_mode", &c.DirMode)
	dur("scan__interval_seconds", &c.ScanInterval)
	dur("scan__settle_seconds", &c.SettleWindow)
	f64("detect__auto_threshold", &c.AutoThreshold)
	f64("detect__review_threshold", &c.ReviewThresh)
	f64("detect__ambiguity_margin", &c.Ambiguity)
	str("detect__pdf_default", &c.PDFDefault)
}

func mark(ro map[string]bool, key string) {
	if ro != nil {
		ro[key] = true
	}
}

// Validate reports problems without being fatal.
//
// A container that exits on a config error is a container whose UI the user
// cannot open to FIX the config error. Start, serve the UI, show the
// problems in red, do no work.
func (c Config) Validate() []string {
	var problems []string
	if c.MaxPlansPerRun <= 0 {
		problems = append(problems, "max_plans_per_run must be positive")
	}
	switch c.Mode {
	case "copy", "link_or_copy", "link":
	default:
		problems = append(problems, "ops__mode must be copy, link_or_copy or link")
	}
	switch c.Collision {
	case "skip", "suffix", "fail":
	default:
		problems = append(problems, "ops__collision must be skip, suffix or fail (there is no overwrite)")
	}
	if c.AutoThreshold < c.ReviewThresh {
		problems = append(problems, "auto_threshold must be at least review_threshold")
	}
	if c.ScanInterval < time.Minute {
		problems = append(problems, "scan interval below 60s will hammer the client")
	}
	return problems
}
