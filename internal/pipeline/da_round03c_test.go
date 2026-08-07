package pipeline

import (
	"context"
	"testing"
)

// The two predicates internal/web/assets/app.js uses to decide which
// buttons a plan card gets. Kept here as literals so a Go test can assert
// against them; if the UI changes, these must change with it.
func uiOffersExecute(status string) bool { return status == "draft" }
func uiOffersUndo(status string) bool {
	return status == "done" || status == "partial" || status == "failed"
}

// DA-13: `blocked` is a terminal state with no exit.
//
// Three individually correct things compose into a dead end:
//
//  1. Execute sets a plan to `blocked` when Preflight refuses — §10.3 calls
//     ENOSPC mid-run "now the primary failure path, not an edge case", and
//     a preflight refusal is the good version of that;
//  2. app.js offers Execute only on `draft` and Undo only on
//     done/partial/failed, so a blocked plan gets NO button;
//  3. UnresolvedPlanFor — this round's DA-11 fix — suppresses every future
//     plan for that orphan while a blocked one exists.
//
// Before (3), the next scan minted a fresh draft. That was the noise DA-11
// removed, and it was also the only way out. Now: the user frees 5 TB, the
// card still says `blocked`, no button appears, and every scan logs
// "already has a blocked plan waiting on you" — for an action the UI does
// not offer. The item cannot be filed again without editing SQLite.
//
// A blocked plan placed nothing: Preflight refuses before the first byte.
// So the fix is to return it to `draft`, exactly as ReleaseStuckPlans
// already does for `executing`.
func TestDA13_BlockedPlanIsADeadEnd(t *testing.T) {
	p, db, g, base := newPipeline(t)
	ctx := context.Background()

	oneOrphan(t, p, db, g, base)
	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	plans, _ := db.ListPlans(ctx, "", 10)
	if len(plans) != 1 {
		t.Fatalf("setup: %d plans", len(plans))
	}

	// Preflight refuses for want of space. No bytes were written.
	if err := db.SetPlanStatus(ctx, plans[0].ID, "blocked",
		"exec: insufficient free space"); err != nil {
		t.Fatal(err)
	}

	// The user frees several terabytes and comes back.
	if uiOffersExecute("blocked") || uiOffersUndo("blocked") {
		t.Fatal("the UI offers a button for a blocked plan; the fixture is stale")
	}

	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListPlans(ctx, "", 10)

	stuck := len(after) == 1 && after[0].Status == "blocked"
	if stuck {
		t.Errorf("DA-13 CONFIRMED: the only plan for this orphan is %q. The UI "+
			"offers neither Execute (draft only) nor Undo (done/partial/failed), "+
			"UnresolvedPlanFor suppresses any replacement, and nothing transitions "+
			"a blocked plan anywhere. The item is unfileable without a database "+
			"edit, and the scan log tells the user it is waiting on them.",
			after[0].Status)
	}
}
