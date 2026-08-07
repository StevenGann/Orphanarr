package store

import (
	"context"
	"encoding/json"
	"time"
)

// Orphan is one discovered download plus what the classifier concluded.
//
// The evidence is persisted, not just the verdict, so that "why did it
// think that was a comic" is answerable after the fact without reproducing
// the scan (DESIGN §10.4).
type Orphan struct {
	ID          int64   `json:"id"`
	ClientID    int64   `json:"client_id"`
	ClientName  string  `json:"client_name"`
	ExternalID  string  `json:"external_id"`
	Name        string  `json:"name"`
	SavePath    string  `json:"save_path"`
	Fingerprint string  `json:"fingerprint"`
	SizeBytes   int64   `json:"size_bytes"`
	State       string  `json:"state"`
	MediaType   string  `json:"media_type"`
	Score       float64 `json:"score"`
	ParseConf   float64 `json:"parse_confidence"`
	Cardinality string  `json:"cardinality"`
	Reason      string  `json:"reason"`

	Signals json.RawMessage `json:"signals"`
	Parsed  json.RawMessage `json:"parsed"`
	Files   json.RawMessage `json:"files"`

	FirstSeen string `json:"first_seen_at"`
	LastSeen  string `json:"last_seen_at"`
}

// UpsertOrphan records or refreshes a discovered orphan.
//
// first_seen_at is deliberately NOT in the update list: the settle window
// is measured from it, and resetting it on every poll would mean nothing
// ever settles.
//
// state is updated CONDITIONALLY. The user's decisions — `ignored` and
// `filed` — are sticky, because a re-scan recomputing "discovered" would
// silently undo them every scan interval and the UI promises "it will be
// skipped on future scans". Everything else is a system verdict and must
// refresh: an orphan blocked by a cross-seed has to stop being blocked
// when the cross-seed goes away.
func (d *DB) UpsertOrphan(ctx context.Context, o Orphan) (int64, error) {
	raw := func(m json.RawMessage, def string) string {
		if len(m) == 0 {
			return def
		}
		return string(m)
	}

	_, err := d.sql.ExecContext(ctx, `
        INSERT INTO orphan(client_id, external_id, name, save_path, fingerprint,
                           size_bytes, state, media_type, score, parse_conf,
                           cardinality, reason, signals_json, parsed_json,
                           files_json, first_seen_at, last_seen_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(client_id, external_id) DO UPDATE SET
            -- Only the USER'S decisions are sticky. System states
            -- (discovered, blocked, unknown) must refresh, or an orphan
            -- blocked by a cross-seed stays blocked forever after the
            -- cross-seed is removed — a decision nobody made, that nothing
            -- reverses, and that looks exactly like the tool ignoring an
            -- item for no reason.
            state=CASE WHEN orphan.state IN ('ignored','filed')
                       THEN orphan.state ELSE excluded.state END,
            name=excluded.name, save_path=excluded.save_path,
            fingerprint=excluded.fingerprint, size_bytes=excluded.size_bytes,
            media_type=excluded.media_type,
            score=excluded.score, parse_conf=excluded.parse_conf,
            cardinality=excluded.cardinality, reason=excluded.reason,
            signals_json=excluded.signals_json, parsed_json=excluded.parsed_json,
            files_json=excluded.files_json, last_seen_at=excluded.last_seen_at`,
		o.ClientID, o.ExternalID, o.Name, o.SavePath, o.Fingerprint,
		o.SizeBytes, o.State, o.MediaType, o.Score, o.ParseConf,
		o.Cardinality, o.Reason, raw(o.Signals, "[]"), raw(o.Parsed, "{}"),
		raw(o.Files, "[]"), now(), now())
	if err != nil {
		return 0, err
	}

	var id int64
	err = d.sql.QueryRowContext(ctx,
		`SELECT id FROM orphan WHERE client_id=? AND external_id=?`,
		o.ClientID, o.ExternalID).Scan(&id)
	return id, err
}

func (d *DB) ListOrphans(ctx context.Context, state string, limit int) ([]Orphan, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT o.id, o.client_id, COALESCE(c.name,''), o.external_id, o.name,
                 o.save_path, o.fingerprint, o.size_bytes, o.state, o.media_type,
                 o.score, o.parse_conf, o.cardinality, o.reason,
                 o.signals_json, o.parsed_json, o.files_json,
                 o.first_seen_at, o.last_seen_at
          FROM orphan o LEFT JOIN client c ON c.id = o.client_id`
	args := []any{}
	if state != "" {
		q += ` WHERE o.state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY o.last_seen_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Orphan{}
	for rows.Next() {
		var o Orphan
		var sig, parsed, files string
		if err := rows.Scan(&o.ID, &o.ClientID, &o.ClientName, &o.ExternalID, &o.Name,
			&o.SavePath, &o.Fingerprint, &o.SizeBytes, &o.State, &o.MediaType,
			&o.Score, &o.ParseConf, &o.Cardinality, &o.Reason,
			&sig, &parsed, &files, &o.FirstSeen, &o.LastSeen); err != nil {
			return nil, err
		}
		o.Signals = json.RawMessage(sig)
		o.Parsed = json.RawMessage(parsed)
		o.Files = json.RawMessage(files)
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetOrphan looks up by primary key.
//
// This used to list 1000 rows and scan them in Go, which returned
// ErrNotFound for a perfectly valid id on any library with more than 1000
// orphans — so /explain 404'd and markFiled silently failed AFTER a
// successful execute. Data-dependent, invisible to every test, and it
// appeared only on the large libraries this tool exists for.
func (d *DB) GetOrphan(ctx context.Context, id int64) (Orphan, error) {
	var o Orphan
	var sig, parsed, files string
	err := d.sql.QueryRowContext(ctx, `
        SELECT o.id, o.client_id, COALESCE(c.name,''), o.external_id, o.name,
               o.save_path, o.fingerprint, o.size_bytes, o.state, o.media_type,
               o.score, o.parse_conf, o.cardinality, o.reason,
               o.signals_json, o.parsed_json, o.files_json,
               o.first_seen_at, o.last_seen_at
        FROM orphan o LEFT JOIN client c ON c.id = o.client_id
        WHERE o.id = ?`, id).Scan(&o.ID, &o.ClientID, &o.ClientName, &o.ExternalID,
		&o.Name, &o.SavePath, &o.Fingerprint, &o.SizeBytes, &o.State, &o.MediaType,
		&o.Score, &o.ParseConf, &o.Cardinality, &o.Reason,
		&sig, &parsed, &files, &o.FirstSeen, &o.LastSeen)
	if err != nil {
		return Orphan{}, ErrNotFound
	}
	o.Signals = json.RawMessage(sig)
	o.Parsed = json.RawMessage(parsed)
	o.Files = json.RawMessage(files)
	return o, nil
}

// OpenPlanFor returns an existing DRAFT plan for an orphan.
//
// savePlan always INSERTed, so at the 15-minute default a single unfiled
// orphan accrued 96 duplicate draft plans a day and the review list — which
// caps at 200 — filled with the same item.
//
// Only 'draft' is reusable, and that restriction is load-bearing.
// SavePlan's update path DELETEs and re-inserts every step, which erases
// created_by_us and created_dirs_json — so reusing a 'failed' or 'blocked'
// plan would destroy the undo record for files its first attempt had
// already placed, leaving them in the library with nothing recording that
// Orphanarr put them there.
func (d *DB) OpenPlanFor(ctx context.Context, orphanID int64) (int64, bool) {
	var id int64
	err := d.sql.QueryRowContext(ctx, `
        SELECT id FROM plan
        WHERE orphan_id = ? AND status = 'draft'
        ORDER BY id DESC LIMIT 1`, orphanID).Scan(&id)
	return id, err == nil
}

// SetOrphanState records a sticky user decision (ignore, filed).
func (d *DB) SetOrphanState(ctx context.Context, id int64, state string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE orphan SET state=? WHERE id=?`, state, id)
	return err
}

// FirstSeen returns when this orphan was first observed, so the settle
// window survives a restart.
func (d *DB) FirstSeen(ctx context.Context, clientID int64, externalID string) (time.Time, bool) {
	var s string
	err := d.sql.QueryRowContext(ctx,
		`SELECT first_seen_at FROM orphan WHERE client_id=? AND external_id=?`,
		clientID, externalID).Scan(&s)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// Plan is an inert, serialisable description of what would happen. Nothing
// in it has occurred.
type Plan struct {
	ID           int64  `json:"id"`
	OrphanID     int64  `json:"orphan_id"`
	OrphanName   string `json:"orphan_name"`
	MediaType    string `json:"media_type"`
	Status       string `json:"status"`
	CopyBytes    int64  `json:"copy_bytes"`
	LinkBytes    int64  `json:"link_bytes"`
	NeedsReview  bool   `json:"needs_review"`
	ReviewReason string `json:"review_reason"`
	Error        string `json:"error"`
	CreatedAt    string `json:"created_at"`
	ExecutedAt   string `json:"executed_at,omitempty"`

	Warnings json.RawMessage `json:"warnings"`
	Steps    []PlanStep      `json:"steps,omitempty"`
}

// PlanStep is one intended file placement.
type PlanStep struct {
	ID           int64  `json:"id"`
	PlanID       int64  `json:"plan_id"`
	Seq          int    `json:"seq"`
	SrcPath      string `json:"src_path"`
	DstPath      string `json:"dst_path"`
	Method       string `json:"method"`
	MethodActual string `json:"method_actual"`
	Bytes        int64  `json:"bytes"`

	SrcDev   uint64 `json:"src_dev"`
	SrcIno   uint64 `json:"src_ino"`
	SrcSize  int64  `json:"src_size"`
	SrcMtime string `json:"src_mtime"`

	CreatedByUs bool     `json:"created_by_us"`
	CreatedDirs []string `json:"created_dirs"`
	Status      string   `json:"status"`
	Error       string   `json:"error"`
}

// SavePlan writes a plan and its steps atomically. A plan whose steps were
// half-written would make rollback lie about what it created.
func (d *DB) SavePlan(ctx context.Context, p Plan) (int64, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	warn := "[]"
	if len(p.Warnings) > 0 {
		warn = string(p.Warnings)
	}

	if p.ID == 0 {
		res, err := tx.ExecContext(ctx, `
            INSERT INTO plan(orphan_id, media_type, status, copy_bytes, link_bytes,
                             warnings_json, needs_review, review_reason, created_at)
            VALUES(?,?,?,?,?,?,?,?,?)`,
			p.OrphanID, p.MediaType, p.Status, p.CopyBytes, p.LinkBytes,
			warn, p.NeedsReview, p.ReviewReason, now())
		if err != nil {
			return 0, err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx, `
            UPDATE plan SET status=?, copy_bytes=?, link_bytes=?, warnings_json=?,
                   needs_review=?, review_reason=?, error=? WHERE id=?`,
			p.Status, p.CopyBytes, p.LinkBytes, warn, p.NeedsReview,
			p.ReviewReason, p.Error, p.ID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM plan_step WHERE plan_id=?`, p.ID); err != nil {
			return 0, err
		}
	}

	for _, s := range p.Steps {
		dirs, _ := json.Marshal(s.CreatedDirs)
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO plan_step(plan_id, seq, src_path, dst_path, method,
                                  method_actual, bytes, src_dev, src_ino, src_size,
                                  src_mtime, created_by_us, created_dirs_json,
                                  status, error)
            VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, s.Seq, s.SrcPath, s.DstPath, s.Method, s.MethodActual, s.Bytes,
			s.SrcDev, s.SrcIno, s.SrcSize, s.SrcMtime, s.CreatedByUs,
			string(dirs), s.Status, s.Error); err != nil {
			return 0, err
		}
	}

	return p.ID, tx.Commit()
}

func (d *DB) ListPlans(ctx context.Context, status string, limit int) ([]Plan, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT p.id, p.orphan_id, COALESCE(o.name,''), p.media_type, p.status,
                 p.copy_bytes, p.link_bytes, p.warnings_json, p.needs_review,
                 p.review_reason, p.error, p.created_at, COALESCE(p.executed_at,'')
          FROM plan p LEFT JOIN orphan o ON o.id = p.orphan_id`
	args := []any{}
	if status != "" {
		q += ` WHERE p.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY p.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Plan{}
	for rows.Next() {
		var p Plan
		var warn string
		if err := rows.Scan(&p.ID, &p.OrphanID, &p.OrphanName, &p.MediaType, &p.Status,
			&p.CopyBytes, &p.LinkBytes, &warn, &p.NeedsReview, &p.ReviewReason,
			&p.Error, &p.CreatedAt, &p.ExecutedAt); err != nil {
			return nil, err
		}
		p.Warnings = json.RawMessage(warn)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) GetPlan(ctx context.Context, id int64) (Plan, error) {
	var p Plan
	var warn string
	err := d.sql.QueryRowContext(ctx, `
        SELECT p.id, p.orphan_id, COALESCE(o.name,''), p.media_type, p.status,
               p.copy_bytes, p.link_bytes, p.warnings_json, p.needs_review,
               p.review_reason, p.error, p.created_at, COALESCE(p.executed_at,'')
        FROM plan p LEFT JOIN orphan o ON o.id = p.orphan_id
        WHERE p.id = ?`, id).Scan(&p.ID, &p.OrphanID, &p.OrphanName, &p.MediaType,
		&p.Status, &p.CopyBytes, &p.LinkBytes, &warn, &p.NeedsReview,
		&p.ReviewReason, &p.Error, &p.CreatedAt, &p.ExecutedAt)
	if err != nil {
		return p, ErrNotFound
	}
	p.Warnings = json.RawMessage(warn)
	p.Steps, err = d.PlanSteps(ctx, id)
	return p, err
}

func (d *DB) PlanSteps(ctx context.Context, planID int64) ([]PlanStep, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT id, plan_id, seq, src_path, dst_path, method, method_actual, bytes,
               src_dev, src_ino, src_size, src_mtime, created_by_us,
               created_dirs_json, status, error
        FROM plan_step WHERE plan_id = ? ORDER BY seq`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PlanStep{}
	for rows.Next() {
		var s PlanStep
		var dirs string
		if err := rows.Scan(&s.ID, &s.PlanID, &s.Seq, &s.SrcPath, &s.DstPath,
			&s.Method, &s.MethodActual, &s.Bytes, &s.SrcDev, &s.SrcIno,
			&s.SrcSize, &s.SrcMtime, &s.CreatedByUs, &dirs, &s.Status,
			&s.Error); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(dirs), &s.CreatedDirs)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SetPlanStatus(ctx context.Context, id int64, status, errMsg string) error {
	executed := ""
	if status == "done" || status == "partial" || status == "failed" {
		executed = now()
	}
	_, err := d.sql.ExecContext(ctx,
		`UPDATE plan SET status=?, error=?, executed_at=NULLIF(?, '') WHERE id=?`,
		status, errMsg, executed, id)
	return err
}

// UpdateStepResult records what the executor actually did, which is not the
// same as what was planned: method_actual differs from method whenever a
// link fell back to a copy.
func (d *DB) UpdateStepResult(ctx context.Context, s PlanStep) error {
	dirs, _ := json.Marshal(s.CreatedDirs)
	_, err := d.sql.ExecContext(ctx, `
        UPDATE plan_step SET method_actual=?, created_by_us=?, created_dirs_json=?,
               status=?, error=?, dst_path=?
        WHERE plan_id=? AND seq=?`,
		s.MethodActual, s.CreatedByUs, string(dirs), s.Status, s.Error, s.DstPath,
		s.PlanID, s.Seq)
	return err
}

// InProgressSteps are what Reconcile() must repair on startup.
func (d *DB) InProgressSteps(ctx context.Context) ([]PlanStep, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT id, plan_id, seq, src_path, dst_path, method, method_actual, bytes,
               src_dev, src_ino, src_size, src_mtime, created_by_us,
               created_dirs_json, status, error
        FROM plan_step WHERE status = 'in_progress' ORDER BY plan_id, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PlanStep{}
	for rows.Next() {
		var s PlanStep
		var dirs string
		if err := rows.Scan(&s.ID, &s.PlanID, &s.Seq, &s.SrcPath, &s.DstPath,
			&s.Method, &s.MethodActual, &s.Bytes, &s.SrcDev, &s.SrcIno,
			&s.SrcSize, &s.SrcMtime, &s.CreatedByUs, &dirs, &s.Status,
			&s.Error); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(dirs), &s.CreatedDirs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// IgnoredFingerprints are the sticky "ignore" decisions, keyed on content
// rather than on an infohash — so re-adding the same payload under a new
// hash, or from a different client, stays ignored.
func (d *DB) IgnoredFingerprints(ctx context.Context) (map[string]bool, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT DISTINCT fingerprint FROM orphan WHERE state='ignored' AND fingerprint != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = true
	}
	return out, rows.Err()
}

// Counts powers the dashboard.
func (d *DB) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	q := func(key, query string) error {
		var n int
		if err := d.sql.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return err
		}
		out[key] = n
		return nil
	}
	if err := q("orphans", `SELECT COUNT(*) FROM orphan`); err != nil {
		return nil, err
	}
	if err := q("unknown", `SELECT COUNT(*) FROM orphan WHERE media_type='' OR media_type='unknown'`); err != nil {
		return nil, err
	}
	if err := q("plans_review", `SELECT COUNT(*) FROM plan WHERE status='draft'`); err != nil {
		return nil, err
	}
	if err := q("plans_done", `SELECT COUNT(*) FROM plan WHERE status='done'`); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkStepInProgress records that a step is being attempted.
//
// It writes ONLY the status and destination. The general UpdateStepResult
// writes created_by_us and created_dirs_json unconditionally, so using it
// here cleared the undo record for every already-placed step of a retried
// plan — making those files permanently unremovable, because rollback keys
// on created_by_us.
func (d *DB) MarkStepInProgress(ctx context.Context, planID int64, seq int, dst string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE plan_step SET status='in_progress', dst_path=? WHERE plan_id=? AND seq=?`,
		dst, planID, seq)
	return err
}

// ReleaseStuckPlans moves plans interrupted mid-execution back to a state
// the user can act on.
//
// A plan left at 'executing' is unreachable from every direction: the UI
// offers Execute only on 'draft' and Undo only on 'done', 'partial' or
// 'failed'. It becomes 'failed' with an explanation, which is the state
// that offers Resume, Roll back and Ignore.
func (d *DB) ReleaseStuckPlans(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, `
        UPDATE plan SET status='failed',
               error='interrupted by a restart; steps were reconciled on startup'
        WHERE status='executing'`)
	return err
}

// UnresolvedPlanFor reports a plan that has stopped and is waiting on the
// user, returning its status.
//
// A failed or blocked plan is deliberately NOT reusable — SavePlan's reuse
// branch deletes and re-inserts every step, which would erase the undo
// record for files the first attempt already placed. But it must not be
// silently duplicated either: without this check every scan minted a fresh
// draft for the same orphan and the review queue refilled with it, which is
// the same harm OpenPlanFor exists to prevent, arriving from the other
// side.
func (d *DB) UnresolvedPlanFor(ctx context.Context, orphanID int64) (string, bool) {
	var status string
	err := d.sql.QueryRowContext(ctx, `
        SELECT status FROM plan
        WHERE orphan_id = ? AND status IN ('failed','blocked','executing','undo_failed')
        ORDER BY id DESC LIMIT 1`, orphanID).Scan(&status)
	return status, err == nil
}
