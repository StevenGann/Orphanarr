// Package store owns persistence. It is the ONLY package that imports
// database/sql (DESIGN §2.3).
//
// SQLite via modernc.org/sqlite, which is pure Go — CGO_ENABLED=0 has to
// hold for the single static binary and the no-QEMU arm64 cross-compile to
// work, and cgo-based drivers cost both.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

// Open connects, applies migrations, and sets the pragmas that decide
// whether this survives a power loss.
func Open(ctx context.Context, path string) (*DB, error) {
	// WAL for concurrent readers while the executor writes; busy_timeout so
	// a scan and an execution overlapping produces a wait rather than an
	// immediate SQLITE_BUSY; foreign_keys because the cascades above are
	// load-bearing and SQLite leaves them OFF by default.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One writer. SQLite serialises writes anyway, and a pool of writers
	// converts that into lock contention plus retry logic nobody reads.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	s := &DB{sql: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the handle for the few callers that need it. Kept narrow on
// purpose — every other package goes through typed methods.
func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) migrate(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            name       TEXT PRIMARY KEY,
            applied_at TEXT NOT NULL
        )`)
	if err != nil {
		return fmt.Errorf("store: migration table: %w", err)
	}

	for _, m := range migrations {
		var seen int
		err := d.sql.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, m.name).Scan(&seen)
		if err != nil {
			return fmt.Errorf("store: check migration %s: %w", m.name, err)
		}
		if seen > 0 {
			continue
		}

		tx, err := d.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?, ?)`,
			m.name, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---------------------------------------------------------------- settings

// GetSetting returns the stored value or def when unset.
func (d *DB) GetSetting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.sql.ExecContext(ctx, `
        INSERT INTO setting(key, value, updated_at) VALUES(?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now())
	return err
}

func (d *DB) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT key, value FROM setting`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------ events

// Event records something worth telling the user about. code is from the
// stable vocabulary; message is prose and may change without breaking
// anything that filters on code.
type Event struct {
	ID      int64
	TS      string
	Level   string
	Code    string
	Message string

	ClientID *int64
	OrphanID *int64
	PlanID   *int64
}

func (d *DB) LogEvent(ctx context.Context, e Event) error {
	if e.Level == "" {
		e.Level = "info"
	}
	_, err := d.sql.ExecContext(ctx, `
        INSERT INTO event(ts, level, code, client_id, orphan_id, plan_id, message)
        VALUES(?, ?, ?, ?, ?, ?, ?)`,
		now(), e.Level, e.Code, e.ClientID, e.OrphanID, e.PlanID, e.Message)
	return err
}

func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.sql.QueryContext(ctx, `
        SELECT id, ts, level, code, message FROM event
        ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Code, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SkipReasonCounts powers the dashboard's "why isn't it doing anything"
// breakdown, which is built entirely from the event-code vocabulary.
func (d *DB) SkipReasonCounts(ctx context.Context) (map[string]int, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT code, COUNT(*) FROM event
        WHERE code LIKE 'SKIP_%' OR code IN ('DISK_SPACE_BLOCKED','SRC_UNREADABLE','SRC_CHANGED')
        GROUP BY code ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return nil, err
		}
		out[code] = n
	}
	return out, rows.Err()
}
