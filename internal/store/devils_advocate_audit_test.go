package store

import (
	"context"
	"path/filepath"
	"testing"
)

// DA-7: "Ignore" is not sticky. The UI promises "Ignored. It will be skipped
// on future scans." Two things falsify that:
//
//  1. UpsertOrphan's ON CONFLICT clause sets state=excluded.state, and
//     ScanNow always passes "discovered", so the next scan resets it;
//  2. scan.Settings.Exclusions.Fingerprints — the field the predicate reads
//     for sticky decisions — is never populated outside a unit test.
//
// The same reset applies to "filed", so an executed orphan is re-planned on
// the next scan unless the qBittorrent tag write happened to succeed.
func TestDA7_IgnoreIsResetByTheNextScan(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.SaveClient(ctx, Client{Name: "qb", BaseURL: "http://x"}); err != nil {
		t.Fatal(err)
	}

	o := Orphan{ClientID: 1, ExternalID: "abc", Name: "Some.Release", State: "discovered"}
	id, err := db.UpsertOrphan(ctx, o)
	if err != nil {
		t.Fatal(err)
	}

	// The user clicks Ignore.
	if err := db.SetOrphanState(ctx, id, "ignored"); err != nil {
		t.Fatal(err)
	}

	// The next scan runs. ScanNow rebuilds the row with state "discovered".
	if _, err := db.UpsertOrphan(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetOrphan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ignored" {
		t.Errorf("DA-7 CONFIRMED: state is %q after one more scan, not \"ignored\". "+
			"The Ignore button's stated effect lasts until the next scan interval.",
			got.State)
	}
}
