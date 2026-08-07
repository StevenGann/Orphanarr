package client

import (
	"reflect"
	"testing"
)

// Tags are joined with ", " — comma AND SPACE.
//
// A naive split(",") leaves a leading space on every tag after the first,
// so an exclusion tag like "orphanarr-ignore" silently never matches and
// the user's opt-out does nothing at all. This is exactly the shape of bug
// that produces "I told it to ignore this and it filed it anyway".
//
// A comma can never appear INSIDE a tag: qBittorrent's Tag::isValid()
// rejects one, so splitting on it is lossless.
func TestSplitTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"manual", []string{"manual"}},
		{"manual, keep, orphanarr-ignore", []string{"manual", "keep", "orphanarr-ignore"}},
		// The trap: without TrimSpace the second and third are " keep" and
		// " orphanarr-ignore", and neither matches anything.
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, , b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitTags(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitTags(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// isComplete folds qBittorrent's completeness rules into one boolean, so
// that scan/ keeps only Orphanarr policy.
//
// The state list is an ALLOWLIST and unknown states fail closed (I11). Both
// the 4.x and 5.x spellings are accepted because both ship in supported
// versions: the source emits stoppedUP/stoppedDL where the published 5.0
// wiki still documents pausedUP/pausedDL.
func TestIsComplete(t *testing.T) {
	complete := func(state string, progress float64, left int64) wireTorrent {
		return wireTorrent{State: state, Progress: progress, AmountLeft: left}
	}

	cases := []struct {
		name string
		in   wireTorrent
		want bool
	}{
		{"uploading", complete("uploading", 1, 0), true},
		{"stalledUP", complete("stalledUP", 1, 0), true},
		{"queuedUP", complete("queuedUP", 1, 0), true},
		{"forcedUP", complete("forcedUP", 1, 0), true},
		{"pausedUP (<=4.x spelling)", complete("pausedUP", 1, 0), true},
		{"stoppedUP (>=5.0 spelling)", complete("stoppedUP", 1, 0), true},

		{"downloading", complete("downloading", 0.5, 100), false},
		{"stalledDL", complete("stalledDL", 0.9, 10), false},

		// progress == 1 is NOT sufficient. checkingUP, moving and
		// checkingResumeData all mean bytes are in motion or the path is
		// about to change, and every one of them can report 1.0.
		{"checkingUP at progress 1", complete("checkingUP", 1, 0), false},
		{"checkingResumeData at progress 1", complete("checkingResumeData", 1, 0), false},
		{"moving at progress 1", complete("moving", 1, 0), false},
		{"metaDL", complete("metaDL", 1, 0), false},

		// Unknown states fail CLOSED. A future qBittorrent release adding a
		// state must not make Orphanarr act on it.
		{"unknown state", complete("someFutureState", 1, 0), false},

		// amount_left corroborates progress.
		{"progress 1 but bytes left", complete("uploading", 1, 4096), false},
		{"progress below 1", complete("uploading", 0.99, 0), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isComplete(c.in); got != c.want {
				t.Errorf("isComplete(%+v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// I14 is a type-level property, not a check somebody has to remember: a
// client that cannot express "no category" must be refused.
func TestCanScanRefusesAClientWithoutCategories(t *testing.T) {
	if err := CanScan(Capabilities{Categories: false, Tags: true}); err == nil {
		t.Fatal("a client that cannot express categories was accepted; on a stock " +
			"Deluge every torrent reads as uncategorised, which under O1 is the " +
			"user's entire seeding library")
	}
	if err := CanScan(Capabilities{Categories: true}); err != nil {
		t.Fatalf("a category-capable client was refused: %v", err)
	}
}

// The nil-Category convention is what makes I14 mechanical rather than
// remembered: Go's zero value for a pointer is nil, so an adapter that
// forgets to set it files NOTHING. The default wrong answer is the safe one.
func TestNilCategoryIsNotAnEmptyCategory(t *testing.T) {
	var it Item
	if it.Category != nil {
		t.Fatal("the zero Item must have a nil Category, so a careless adapter " +
			"files nothing rather than everything")
	}
	empty := ""
	it.Category = &empty
	if it.Category == nil || *it.Category != "" {
		t.Fatal("an explicitly empty category must be distinguishable from nil")
	}
}
