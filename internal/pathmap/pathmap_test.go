package pathmap

import (
	"errors"
	"testing"
)

// Longest prefix wins, regardless of declaration order.
//
// Without the sort, a rule for /downloads shadows a more specific one for
// /downloads/movies purely by the order the user happened to add them —
// a configuration bug they cannot see and cannot debug.
func TestLongestPrefixWinsRegardlessOfOrder(t *testing.T) {
	m := New([]Rule{
		{Remote: "/downloads", Local: "/data/torrents"},
		{Remote: "/downloads/movies", Local: "/data/movies"},
	})

	got, err := m.ToLocal("/downloads/movies/The.Matrix/file.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/data/movies/The.Matrix/file.mkv"; got != want {
		t.Errorf("got %q, want %q — the more specific rule must win even when "+
			"it was declared second", got, want)
	}
}

func TestMappingCases(t *testing.T) {
	m := New([]Rule{
		{Remote: "/downloads", Local: "/data/torrents"},
		{Remote: "/seedbox/done/", Local: "/data/sb"},
	})

	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"exact root", "/downloads", "/data/torrents", false},
		{"nested", "/downloads/a/b.mkv", "/data/torrents/a/b.mkv", false},
		{"trailing slash in rule", "/seedbox/done/x.mkv", "/data/sb/x.mkv", false},
		{"uncovered path refuses", "/elsewhere/x.mkv", "", true},
		{"empty refuses", "", "", true},
		// A shared PREFIX is not containment. /downloads-old must not be
		// rewritten by the /downloads rule, or files land in a directory
		// that has nothing to do with them.
		{"sibling with shared prefix refuses", "/downloads-old/x.mkv", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.ToLocal(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected a refusal, got %q", got)
				}
				var unmapped *ErrUnmapped
				if !errors.As(err, &unmapped) {
					t.Errorf("expected ErrUnmapped, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// An unmapped path must ABORT rather than be used as-is. Creating
// directories at a bogus location is how a tool "does nothing" for two
// years while quietly filling a stray folder.
func TestUnmappedIsARefusalNotAPassthrough(t *testing.T) {
	m := New([]Rule{{Remote: "/downloads", Local: "/data"}})
	if got, err := m.ToLocal("/somewhere/else"); err == nil {
		t.Fatalf("an unmapped path was translated to %q instead of refusing", got)
	}
}

// The identity mapper passes paths through, which is the common deployment:
// the container mounts the client's download folder at the path the client
// already reports.
func TestIdentityPassesThrough(t *testing.T) {
	m := Identity()
	got, err := m.ToLocal("/data/torrents/x.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/data/torrents/x.mkv" {
		t.Errorf("got %q, want the path unchanged", got)
	}
	// But an empty path is still a refusal, not "".
	if _, err := m.ToLocal(""); err == nil {
		t.Error("the identity mapper accepted an empty path")
	}
}
