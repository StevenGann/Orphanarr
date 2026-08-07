package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/StevenGann/Orphanarr/internal/client"
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
	pl, err := p.db.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	if pl.Status == "done" {
		return errors.New("plan has already been executed")
	}
	if p.cfg.DryRun {
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

	steps := make([]exec.Step, 0, len(pl.Steps))
	for _, s := range pl.Steps {
		mt, _ := time.Parse(time.RFC3339Nano, s.SrcMtime)
		steps = append(steps, exec.Step{
			Seq: s.Seq, Src: s.SrcPath, Dst: s.DstPath,
			Method:   exec.Method(s.Method),
			Bytes:    s.Bytes,
			SrcDev:   s.SrcDev,
			SrcIno:   s.SrcIno,
			SrcSize:  s.SrcSize,
			SrcMtime: mt,
		})
	}

	// The hardlink fast path is enabled per pair, only where the probe has
	// actually passed. Never globally: an all-or-nothing switch forces
	// copies onto provably linkable pairs.
	allowLink := false
	if r, ok := p.prober.Get(path.Dir(steps[0].Src), lib.Root); ok && r.Outcome == probe.Available {
		allowLink = true
	}

	opts := exec.DefaultOptions()
	opts.AllowLink = allowLink
	opts.Collision = p.cfg.Collision
	opts.FileMode = 0o644
	opts.DirMode = 0o755
	opts.ReserveBytes = p.cfg.ReserveBytes
	opts.ReserveFraction = p.cfg.ReserveFraction

	ex := exec.New(p.fs, newJournal(p.db, planID), opts)

	// Preflight before anything is written, and report the numbers. A tool
	// that fills someone's array at 3am and then cannot write its own
	// database is a tool that gets uninstalled.
	if err := ex.Preflight(steps, lib.Root); err != nil {
		p.db.SetPlanStatus(ctx, planID, "blocked", err.Error())
		code := "DISK_SPACE_BLOCKED"
		if errors.Is(err, exec.ErrNoSpace) {
			// keep the code
		} else {
			code = "SRC_UNREADABLE"
		}
		p.db.LogEvent(ctx, store.Event{
			Level: "warn", Code: code, PlanID: &planID, Message: err.Error(),
		})
		return err
	}

	p.db.SetPlanStatus(ctx, planID, "executing", "")
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
		p.db.UpdateStepResult(ctx, store.PlanStep{
			PlanID: planID, Seq: s.Seq, DstPath: s.Dst,
			MethodActual: string(s.Actual),
			CreatedByUs:  s.CreatedByUs,
			CreatedDirs:  s.CreatedDirs,
			Status:       s.Status,
			Error:        s.Err,
		})
	}

	if runErr != nil {
		p.db.SetPlanStatus(ctx, planID, "failed", runErr.Error())
		p.db.LogEvent(ctx, store.Event{
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
	p.db.SetPlanStatus(ctx, planID, status, "")
	p.db.SetOrphanState(ctx, pl.OrphanID, "filed")

	// Mark the item in the client, best effort. A marker failure NEVER
	// fails a plan or triggers a rollback — the files are placed, and
	// undoing that because a tag did not stick would be absurd.
	if p.cfg.ClientWrite == "tag" {
		if err := p.markFiled(ctx, pl.OrphanID); err != nil {
			p.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "CLIENT_MARK_FAILED", PlanID: &planID,
				Message: err.Error(),
			})
		}
	}

	p.db.LogEvent(ctx, store.Event{
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
	pl, err := p.db.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	if pl.Status != "done" && pl.Status != "partial" && pl.Status != "failed" {
		return fmt.Errorf("plan is %s; only an executed plan can be undone", pl.Status)
	}

	steps := make([]exec.Step, 0, len(pl.Steps))
	for _, s := range pl.Steps {
		steps = append(steps, exec.Step{
			Seq: s.Seq, Src: s.SrcPath, Dst: s.DstPath,
			CreatedByUs: s.CreatedByUs,
			CreatedDirs: s.CreatedDirs,
		})
	}

	ex := exec.New(p.fs, nil, exec.DefaultOptions())
	if errs := ex.Rollback(steps); len(errs) > 0 {
		msg := fmt.Sprintf("%d of %d removals failed: %v", len(errs), len(steps), errs[0])
		p.db.SetPlanStatus(ctx, planID, "undo_failed", msg)
		return errors.New(msg)
	}

	p.db.SetPlanStatus(ctx, planID, "undone", "")
	p.db.SetOrphanState(ctx, pl.OrphanID, "discovered")
	p.db.LogEvent(ctx, store.Event{
		Code: "PLAN_UNDONE", PlanID: &planID,
		Message: pl.OrphanName + ": removed what we created",
	})
	return nil
}

// Reconcile repairs unclean exits before any new work runs.
func (p *Pipeline) Reconcile(ctx context.Context) error {
	rows, err := p.db.InProgressSteps(ctx)
	if err != nil || len(rows) == 0 {
		return err
	}

	steps := make([]exec.Step, 0, len(rows))
	for _, s := range rows {
		steps = append(steps, exec.Step{
			Seq: s.Seq, Src: s.SrcPath, Dst: s.DstPath,
			SrcSize: s.SrcSize, Status: s.Status,
			CreatedByUs: s.CreatedByUs, CreatedDirs: s.CreatedDirs,
		})
	}

	ex := exec.New(p.fs, nil, exec.DefaultOptions())
	errs := ex.Reconcile(steps)

	for i, s := range steps {
		p.db.UpdateStepResult(ctx, store.PlanStep{
			PlanID: rows[i].PlanID, Seq: s.Seq, DstPath: s.Dst,
			MethodActual: string(s.Actual), CreatedByUs: s.CreatedByUs,
			CreatedDirs: s.CreatedDirs, Status: s.Status,
		})
	}

	p.db.LogEvent(ctx, store.Event{
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

// journal writes every mutation to the database before it is attempted and
// again after it succeeds (I7).
type journal struct {
	db     *store.DB
	planID int64
}

func newJournal(db *store.DB, planID int64) *journal {
	return &journal{db: db, planID: planID}
}

func (j *journal) Before(ctx context.Context, s *exec.Step) error {
	return j.db.UpdateStepResult(ctx, store.PlanStep{
		PlanID: j.planID, Seq: s.Seq, DstPath: s.Dst, Status: "in_progress",
	})
}

func (j *journal) After(ctx context.Context, s *exec.Step) error {
	return j.db.UpdateStepResult(ctx, store.PlanStep{
		PlanID: j.planID, Seq: s.Seq, DstPath: s.Dst,
		MethodActual: string(s.Actual), CreatedByUs: s.CreatedByUs,
		CreatedDirs: s.CreatedDirs, Status: "done",
	})
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

	// The uncategorised count is the whole pitch, delivered at the moment
	// the user connects the client rather than after their first scan.
	items, err := impl.ListItems(ctx)
	if err != nil {
		out["warning"] = "connected, but listing items failed: " + err.Error()
		return out, nil
	}
	uncategorised, complete := 0, 0
	var bytes int64
	for _, it := range items {
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
