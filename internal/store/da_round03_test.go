package store

import (
	"context"
	"path/filepath"
	"testing"
)

// DA-8 (round-03 regression): plan reuse destroys the undo record of a
// FAILED plan's completed steps.
//
// OpenPlanFor matches status IN ('draft','blocked','failed'). A failed plan
// is the one state in that set whose steps have already placed files:
// §10.3 calls ENOSPC mid-copy "now the primary failure path, not an edge
// case", and §6.7 leaves such a plan at `failed` with Resume / Roll back /
// Ignore.
//
// SavePlan's reuse branch does DELETE FROM plan_step, then re-inserts every
// step as `pending` with created_by_us = 0. So one scan tick between the
// failure and the user clicking anything erases the record of the files
// already on disk.
//
// Two consequences, both of which the round-03 fixes were written to
// prevent:
//
//   - Rollback keys on created_by_us, so Undo now silently leaves every
//     placed file behind while reporting success;
//   - Execute's new resume filter keys on status == "done", so a resume
//     re-runs from step 0 — which under collision: suffix duplicates every
//     already-placed file, the exact defect NB-8's fix removed.
func TestDA8_PlanReuseErasesTheUndoRecordOfAFailedPlan(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.SaveClient(ctx, Client{Name: "qb", BaseURL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	orphanID, err := db.UpsertOrphan(ctx, Orphan{
		ClientID: 1, ExternalID: "abc", Name: "Show.S01.1080p", State: "discovered",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A plan of two steps is executed; the first places a file, the second
	// fails for want of space.
	planID, err := db.SavePlan(ctx, Plan{
		OrphanID: orphanID, MediaType: "tv", Status: "draft",
		Steps: []PlanStep{
			{Seq: 0, SrcPath: "/d/e1.mkv", DstPath: "/m/S01E01.mkv", SrcSize: 10, Status: "pending"},
			{Seq: 1, SrcPath: "/d/e2.mkv", DstPath: "/m/S01E02.mkv", SrcSize: 10, Status: "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateStepResult(ctx, PlanStep{
		PlanID: planID, Seq: 0, DstPath: "/m/S01E01.mkv",
		MethodActual: "copy", CreatedByUs: true,
		CreatedDirs: []string{"/m/Show (2019)"}, Status: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPlanStatus(ctx, planID, "failed", "ENOSPC"); err != nil {
		t.Fatal(err)
	}

	// The 15-minute scan ticks before the user clicks Resume or Roll back.
	//
	// A FAILED plan must not be offered for reuse: SavePlan's reuse branch
	// deletes and re-inserts every step, which would erase created_by_us
	// for the files the first attempt already placed.
	if reuse, ok := db.OpenPlanFor(ctx, orphanID); ok {
		t.Fatalf("OpenPlanFor offered failed plan %d for reuse; its steps would be "+
			"wiped, leaving %d placed files in the library with no undo record",
			reuse, 1)
	}

	steps, err := db.PlanSteps(ctx, planID)
	if err != nil {
		t.Fatal(err)
	}
	if !steps[0].CreatedByUs {
		t.Errorf("DA-8 CONFIRMED: created_by_us on the completed step is now false. " +
			"Rollback skips it, so Undo reports success and leaves the placed " +
			"file in the library with no record of it.")
	}
	if len(steps[0].CreatedDirs) == 0 {
		t.Errorf("DA-8 CONFIRMED: created_dirs_json was erased, so the directories " +
			"this plan made can never be removed.")
	}
	if steps[0].Status != "done" {
		t.Errorf("DA-8 CONFIRMED: step 0 is %q, not \"done\". Execute's resume "+
			"filter skips only \"done\" steps, so a resume re-runs it — under "+
			"collision: suffix that writes a second copy of a file already placed.",
			steps[0].Status)
	}
}
