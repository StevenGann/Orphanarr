package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/StevenGann/Orphanarr/internal/client"
	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/exec"
	"github.com/StevenGann/Orphanarr/internal/probe"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// Execute runs an approved plan.
//
// It is a separate, explicitly-invoked step. Scanning produces plans and
// nothing else; this is the only path from a plan to a file, and it refuses
// before writing a byte rather than part-way through.
func (p *Pipeline) Execute(ctx context.Context, planID int64) error {
	// One execution at a time. DESIGN §2.5 specifies a single serialized
	// executor; without this two POSTs both pass the status check and race
	// on the same destinations, where the loser's cleanup unlinks the
	// winner's partial.
	p.execMu.Lock()
	defer p.execMu.Unlock()

	// Recovery and audit writes must survive the request being abandoned.
	// A browser tab closed mid-copy used to cancel the copy AND every
	// subsequent status write, leaving the plan stuck at "executing" with
	// no error recorded — so the journal died in exactly the scenario it
	// exists for.
	rec := context.WithoutCancel(ctx)

	pl, err := p.db.GetPlan(rec, planID)
	if err != nil {
		return err
	}
	if pl.Status == "done" {
		return errors.New("plan has already been executed")
	}
	if p.Config().DryRun {
		// The guard would refuse anyway, but failing here gives the user a
		// sentence about WHY rather than an errno from three layers down.
		return errors.New("dry run is on: turn it off in Settings before executing a plan")
	}
	if len(pl.Steps) == 0 {
		return errors.New("plan has no steps")
	}

	lib, ok := p.libraryForName(pl.MediaType)
	if !ok {
		return fmt.Errorf("no library configured for %s", pl.MediaType)
	}

	// I1 is enforced by matching against REGISTERED source roots, and under
	// identity path-mapping the only thing that registers them is a scan.
	// So a fresh process — restarted, with a plan persisted before the
	// restart and the first scan tick fifteen minutes away — would execute
	// with zero source roots and I1's check unable to fire.
	//
	// Refusing costs the user one "Scan now" click. Proceeding costs I1 in
	// exactly the library-overlaps-download misconfiguration the invariant
	// exists for.
	for _, s := range pl.Steps {
		if s.SrcPath == "" {
			continue
		}
		if _, ok := p.guard.SourceRootFor(s.SrcPath); !ok {
			return fmt.Errorf("this plan's download root is not registered yet, so the "+
				"source-file protection is not armed for %s. Run a scan first — that "+
				"is what registers it", s.SrcPath)
		}
		break
	}

	steps := toExecSteps(pl.Steps)

	// Skip steps a previous attempt already completed.
	//
	// Re-executing a failed plan restarted at step 0, which under the
	// suffix collision policy produced "Foo (2).mkv" beside every
	// already-placed file — a full duplicate of the completed portion, at
	// exactly the moment the plan failed for want of space. This is §6.7's
	// Resume button.
	pending := make([]exec.Step, 0, len(steps))
	for _, s := range steps {
		if s.Status == "done" {
			continue
		}
		pending = append(pending, s)
	}
	if len(pending) == 0 {
		p.db.SetPlanStatus(rec, planID, "done", "")
		return nil
	}
	steps = pending

	// The hardlink fast path is enabled per pair, only where the probe has
	// actually passed. Never globally: an all-or-nothing switch forces
	// copies onto provably linkable pairs.
	//
	// The lookup key must be the REGISTERED SOURCE ROOT, the same key
	// savePlan cached under. Looking it up under path.Dir(steps[0].Src) —
	// the torrent's own subfolder — always missed, so every plan that said
	// "hardlink" executed as a full copy and the difference was invisible
	// except in method_actual. Two halves of one decision keyed
	// differently is a bug that cannot be seen from either half.
	allowLink := false
	if root, ok := p.guard.SourceRootFor(steps[0].Src); ok {
		if r, cached := p.prober.Get(root, lib.Root); cached && r.Outcome == probe.Available {
			allowLink = true
		}
	}

	cfg := p.Config()
	opts := exec.DefaultOptions()
	opts.AllowLink = allowLink
	opts.Collision = cfg.Collision
	// From configuration, not hardcoded. These two keys were parsed,
	// validated, rendered in the UI with help text and read by nothing —
	// which is worse than an absent knob, because the UI implied they did
	// something.
	opts.FileMode = fs.FileMode(cfg.FileMode)
	opts.DirMode = fs.FileMode(cfg.DirMode)
	opts.ReserveBytes = cfg.ReserveBytes
	opts.ReserveFraction = cfg.ReserveFraction

	ex := exec.New(p.fs, newJournal(p.db, p.ndjson, planID), opts)

	// Preflight before anything is written, and report the numbers. A tool
	// that fills someone's array at 3am and then cannot write its own
	// database is a tool that gets uninstalled.
	if err := ex.Preflight(steps, lib.Root); err != nil {
		p.db.SetPlanStatus(rec, planID, "blocked", err.Error())
		code := "DISK_SPACE_BLOCKED"
		if errors.Is(err, exec.ErrNoSpace) {
			// keep the code
		} else {
			code = "SRC_UNREADABLE"
		}
		p.db.LogEvent(rec, store.Event{
			Level: "warn", Code: code, PlanID: &planID, Message: err.Error(),
		})
		return err
	}

	p.db.SetPlanStatus(rec, planID, "executing", "")
	runErr := ex.Run(ctx, steps)

	// Record what ACTUALLY happened, per step. method_actual differs from
	// method whenever a link fell back to a copy, and rollback keys on
	// created_by_us — so a step whose result is not written back is a step
	// rollback cannot undo.
	partial := false
	for _, s := range steps {
		if s.Status == "skipped" {
			partial = true
		}
		p.db.UpdateStepResult(rec, store.PlanStep{
			PlanID: planID, Seq: s.Seq, DstPath: s.Dst,
			MethodActual: string(s.Actual),
			CreatedByUs:  s.CreatedByUs,
			CreatedDirs:  s.CreatedDirs,
			Status:       s.Status,
			Error:        s.Err,
		})
	}

	if runErr != nil {
		p.db.SetPlanStatus(rec, planID, "failed", runErr.Error())
		p.db.LogEvent(rec, store.Event{
			Level: "error", Code: "PLAN_FAILED", PlanID: &planID,
			Message: runErr.Error(),
		})
		// Auto-rollback is deliberately OFF for a clean in-run failure:
		// the user may prefer to free space and resume rather than lose
		// hours of completed copying. The plan sits in `failed` with
		// Resume, Roll back and Ignore.
		return runErr
	}

	status := "done"
	if partial {
		// A skip is not a success. `partial` is a visible, actionable
		// state and must not be reported as completion.
		status = "partial"
	}
	p.db.SetPlanStatus(rec, planID, status, "")
	p.db.SetOrphanState(rec, pl.OrphanID, "filed")

	// Mark the item in the client, best effort. A marker failure NEVER
	// fails a plan or triggers a rollback — the files are placed, and
	// undoing that because a tag did not stick would be absurd.
	if cfg.ClientWrite == "tag" {
		if err := p.markFiled(rec, pl.OrphanID); err != nil {
			p.db.LogEvent(rec, store.Event{
				Level: "warn", Code: "CLIENT_MARK_FAILED", PlanID: &planID,
				Message: err.Error(),
			})
		}
	}

	p.db.LogEvent(rec, store.Event{
		Code: "PLAN_EXECUTED", PlanID: &planID,
		Message: fmt.Sprintf("%s: %d steps, %s", pl.OrphanName, len(steps),
			humanBytes(pl.CopyBytes+pl.LinkBytes)),
	})
	return nil
}

// Undo removes what a plan created, in reverse order.
//
// Because sources are never deleted, a completed undo returns the
// filesystem to its exact pre-plan state — which is the whole reason the
// design only ever adds.
func (p *Pipeline) Undo(ctx context.Context, planID int64) error {
	p.execMu.Lock()
	defer p.execMu.Unlock()

	rec := context.WithoutCancel(ctx)
	pl, err := p.db.GetPlan(rec, planID)
	if err != nil {
		return err
	}
	if pl.Status != "done" && pl.Status != "partial" && pl.Status != "failed" {
		return fmt.Errorf("plan is %s; only an executed plan can be undone", pl.Status)
	}

	// Carry the FULL identity, not just the paths. Rollback verifies the
	// destination is still what we placed before unlinking it, and a step
	// reconstructed without src_size gives it nothing to verify against —
	// which it now correctly refuses rather than defaulting to a delete.
	steps := toExecSteps(pl.Steps)

	ex := exec.New(p.fs, nil, exec.DefaultOptions())
	if errs := ex.Rollback(steps); len(errs) > 0 {
		msg := fmt.Sprintf("%d of %d removals failed: %v", len(errs), len(steps), errs[0])
		p.db.SetPlanStatus(rec, planID, "undo_failed", msg)
		return errors.New(msg)
	}

	p.db.SetPlanStatus(rec, planID, "undone", "")
	p.db.SetOrphanState(rec, pl.OrphanID, "discovered")
	p.db.LogEvent(rec, store.Event{
		Code: "PLAN_UNDONE", PlanID: &planID,
		Message: pl.OrphanName + ": removed what we created",
	})
	return nil
}

// Reconcile repairs unclean exits before any new work runs.
func (p *Pipeline) Reconcile(ctx context.Context) error {
	rec := context.WithoutCancel(ctx)
	rows, err := p.db.InProgressSteps(rec)
	if err != nil || len(rows) == 0 {
		return err
	}

	steps := toExecSteps(rows)

	ex := exec.New(p.fs, nil, exec.DefaultOptions())
	errs := ex.Reconcile(steps)

	for i, s := range steps {
		p.db.UpdateStepResult(rec, store.PlanStep{
			PlanID: rows[i].PlanID, Seq: s.Seq, DstPath: s.Dst,
			MethodActual: string(s.Actual), CreatedByUs: s.CreatedByUs,
			CreatedDirs: s.CreatedDirs, Status: s.Status,
		})
	}

	// Free plans that were interrupted mid-execution. Without this a
	// crashed plan stays at "executing" forever: the UI offers Execute
	// only on draft and Undo only on done/partial/failed, so it is
	// unreachable from every direction.
	if err := p.db.ReleaseStuckPlans(rec); err != nil {
		errs = append(errs, err)
	}

	p.db.LogEvent(rec, store.Event{
		Code: "RECONCILED",
		Message: fmt.Sprintf("repaired %d interrupted steps, %d errors",
			len(steps), len(errs)),
	})
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (p *Pipeline) markFiled(ctx context.Context, orphanID int64) error {
	o, err := p.db.GetOrphan(ctx, orphanID)
	if err != nil {
		return err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, e := range p.clients {
		if e.Cfg.ID != o.ClientID {
			continue
		}
		return e.Client.MarkFiled(ctx, clientID(o.ExternalID), "orphanarr-filed")
	}
	return errors.New("client no longer configured")
}

// toExecSteps is the ONE conversion from a stored step to an executable
// one.
//
// There were four hand-written copies of this, each dropping a different
// subset: Undo dropped SrcSize and SrcMtime, so Rollback had no identity to
// verify; Reconcile dropped Method and Bytes. Every new field had to be
// added in four places and forgetting one was silent. It is one function
// now so that it cannot be.
func toExecSteps(rows []store.PlanStep) []exec.Step {
	out := make([]exec.Step, 0, len(rows))
	for _, s := range rows {
		mt, _ := time.Parse(time.RFC3339Nano, s.SrcMtime)
		out = append(out, exec.Step{
			Seq: s.Seq, Src: s.SrcPath, Dst: s.DstPath,
			Method:      exec.Method(s.Method),
			Actual:      exec.Method(s.MethodActual),
			Bytes:       s.Bytes,
			SrcDev:      s.SrcDev,
			SrcIno:      s.SrcIno,
			SrcSize:     s.SrcSize,
			SrcMtime:    mt,
			CreatedByUs: s.CreatedByUs,
			CreatedDirs: s.CreatedDirs,
			Status:      s.Status,
			Err:         s.Error,
		})
	}
	return out
}

// TestClient probes an unsaved configuration.
//
// It reports whether the instance can be reached, what it is, and — the
// part that matters — whether it can express "this item has no category".
// A client that cannot is refused rather than scanned (I14), and the user
// should learn that here rather than after a scan has selected everything
// they own.
func (p *Pipeline) TestClient(ctx context.Context, c store.Client) (map[string]any, error) {
	impl, err := client.New(client.Config{
		ID: c.ID, Name: c.Name, Kind: c.Kind, BaseURL: c.BaseURL,
		Username: c.Username, Password: c.Password, APIKey: c.APIKey,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	info, err := impl.Probe(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"app_version": info.AppVersion,
		"api_version": info.APIVersion,
		"auth_mode":   info.AuthMode,
		"caps": map[string]bool{
			"categories": info.Caps.Categories,
			"tags":       info.Caps.Tags,
			"file_list":  info.Caps.FileList,
		},
		"scannable": true,
	}
	if err := client.CanScan(info.Caps); err != nil {
		out["scannable"] = false
		out["refusal"] = err.Error()
		return out, nil
	}

	items, err := impl.ListItems(ctx)
	if err != nil {
		out["warning"] = "connected, but listing items failed: " + err.Error()
		return out, nil
	}
	uncategorised, complete := 0, 0
	var bytes int64
	for _, it := range items {
		// Match the predicate exactly: whitespace is a category, so the
		// connect screen's headline number cannot disagree with what a
		// scan will actually find.
		if it.Category != nil && *it.Category == "" {
			uncategorised++
			if it.Complete {
				complete++
				bytes += it.SizeBytes
			}
		}
	}
	out["total"] = len(items)
	out["uncategorised"] = uncategorised
	out["uncategorised_complete"] = complete
	out["uncategorised_bytes"] = bytes
	return out, nil
}

// ApplyConfig reloads settings from the database and installs them live.
//
// Without this every settings save was inert until a restart: both
// SetConfig methods existed and neither had a caller, so a user turning
// dry-run off and clicking Execute was still told "dry run is on".
func (p *Pipeline) ApplyConfig(ctx context.Context) (config.Config, error) {
	cfg, err := config.Load(ctx, p.db)
	if err != nil {
		return cfg, err
	}
	// Preserve the process-scoped values, which are not stored settings.
	old := p.Config()
	cfg.ConfigDir, cfg.Addr = old.ConfigDir, old.Addr
	if cfg.APIKey == "" {
		cfg.APIKey = old.APIKey
	}

	wasDry := old.DryRun
	p.SetConfig(cfg)

	// Turning dry-run OFF makes probing legal for the first time. Without
	// this the badge stays at "not probed — dry run is on" until a restart
	// or an unrelated client save, which reads as a broken probe rather
	// than a stale one.
	if wasDry && !cfg.DryRun {
		p.probeAllPairs(ctx)
	}
	return cfg, nil
}
