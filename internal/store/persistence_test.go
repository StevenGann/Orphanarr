package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedClient(t *testing.T, db *DB) int64 {
	t.Helper()
	id, err := db.SaveClient(context.Background(), Client{
		Name: "qb", Kind: "qbittorrent", BaseURL: "http://localhost:8080", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A re-scan must NOT undo the user's sticky decisions.
//
// UpsertOrphan used to include state in its update list while ScanNow always
// computed "discovered", so Ignore survived at most one scan interval and a
// filed orphan was re-planned 15 minutes later. The UI promises "it will be
// skipped on future scans"; this is what makes that true.
func TestRescanPreservesStickyState(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)

	o := Orphan{ClientID: cid, ExternalID: "abc", Name: "Some.Release", State: "discovered"}
	id, err := db.UpsertOrphan(ctx, o)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.SetOrphanState(ctx, id, "ignored"); err != nil {
		t.Fatal(err)
	}

	// The next scan sees the same torrent and recomputes "discovered".
	o.State = "discovered"
	o.Name = "Some.Release.Renamed"
	if _, err := db.UpsertOrphan(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetOrphan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ignored" {
		t.Errorf("state = %q after a re-scan, want ignored", got.State)
	}
	// Everything else SHOULD refresh.
	if got.Name != "Some.Release.Renamed" {
		t.Errorf("name did not refresh: %q", got.Name)
	}
}

// first_seen_at drives the settle window and must survive a re-scan, or
// nothing ever settles.
func TestFirstSeenIsStable(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)

	o := Orphan{ClientID: cid, ExternalID: "abc", Name: "X"}
	if _, err := db.UpsertOrphan(ctx, o); err != nil {
		t.Fatal(err)
	}
	first, ok := db.FirstSeen(ctx, cid, "abc")
	if !ok {
		t.Fatal("FirstSeen did not find the orphan it just stored")
	}

	if _, err := db.UpsertOrphan(ctx, o); err != nil {
		t.Fatal(err)
	}
	second, _ := db.FirstSeen(ctx, cid, "abc")
	if !first.Equal(second) {
		t.Errorf("first_seen_at moved on re-scan: %s -> %s", first, second)
	}
}

// GetOrphan must be a primary-key lookup.
//
// It used to list 1000 rows and scan them in Go, so on a library with more
// than 1000 orphans it returned ErrNotFound for a valid id — /explain 404'd
// and markFiled failed silently AFTER a successful execute.
func TestGetOrphanFindsRowsBeyondTheOldListLimit(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)

	var wanted int64
	for i := 0; i < 1200; i++ {
		id, err := db.UpsertOrphan(ctx, Orphan{
			ClientID: cid, ExternalID: string(rune('a'+i%26)) + itoa(i), Name: "n",
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 5 { // an early row, which the old LIMIT-ordered scan missed
			wanted = id
		}
	}

	if _, err := db.GetOrphan(ctx, wanted); err != nil {
		t.Fatalf("GetOrphan could not find id %d among 1200 rows: %v", wanted, err)
	}
}

// Deleting a client must not destroy the undo history.
//
// The schema cascaded from client to orphan to plan to plan_step, so
// removing a client silently deleted every plan belonging to it — at
// exactly the moment a user is most likely to want to undo something.
func TestDeletingAClientPreservesPlanHistory(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)

	oid, err := db.UpsertOrphan(ctx, Orphan{ClientID: cid, ExternalID: "x", Name: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SavePlan(ctx, Plan{
		OrphanID: oid, MediaType: "movie", Status: "done",
		Steps: []PlanStep{{Seq: 0, SrcPath: "/a", DstPath: "/b", CreatedByUs: true, SrcSize: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteClient(ctx, cid); err != nil {
		t.Fatal(err)
	}

	p, err := db.GetPlan(ctx, pid)
	if err != nil {
		t.Fatalf("the plan was destroyed with its client: %v", err)
	}
	if len(p.Steps) != 1 {
		t.Fatalf("the plan's steps were destroyed: %d remain", len(p.Steps))
	}
	if !p.Steps[0].CreatedByUs || p.Steps[0].SrcSize != 10 {
		t.Error("the step lost the fields Undo needs to verify before removing a file")
	}
}

// An open plan is reused rather than duplicated on every scan. At the
// 15-minute default, always inserting produced 96 draft plans per orphan
// per day in a review list that caps at 200.
func TestOpenPlanIsReused(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)
	oid, _ := db.UpsertOrphan(ctx, Orphan{ClientID: cid, ExternalID: "x", Name: "M"})

	first, err := db.SavePlan(ctx, Plan{OrphanID: oid, MediaType: "movie", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := db.OpenPlanFor(ctx, oid)
	if !ok || got != first {
		t.Fatalf("OpenPlanFor = %d, %v; want %d, true", got, ok, first)
	}

	// An executed plan is NOT reusable — a later scan should be able to
	// produce a fresh one.
	if err := db.SetPlanStatus(ctx, first, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.OpenPlanFor(ctx, oid); ok {
		t.Error("a completed plan was offered for reuse")
	}
}

// Sticky ignores are keyed on content, so the same payload re-added under a
// different infohash stays ignored.
func TestIgnoredFingerprintsAreContentKeyed(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	cid := seedClient(t, db)

	id, _ := db.UpsertOrphan(ctx, Orphan{
		ClientID: cid, ExternalID: "hash-v1", Name: "M", Fingerprint: "FP",
	})
	db.SetOrphanState(ctx, id, "ignored")

	set, err := db.IgnoredFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !set["FP"] {
		t.Fatal("an ignored orphan's fingerprint was not returned, so re-adding " +
			"the same payload under a new infohash would file it")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
