package layout

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The adversarial corpus is hostile input a torrent can actually supply.
// Every case here is a way a name can redirect, corrupt or truncate a write,
// and the expectation is an ACTION — refuse, sanitise, truncate — rather
// than a media type.

type advCase struct {
	ID     string         `json:"id"`
	Input  string         `json:"input"`
	Shape  string         `json:"shape"`
	Target string         `json:"target"`
	Expect map[string]any `json:"expect"`
	Why    string         `json:"why"`
}

func loadAdversarial(t *testing.T) []advCase {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "tests", "corpus", "adversarial.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out []advCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c advCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("%v\n%s", err, line)
		}
		out = append(out, c)
	}
	return out
}

func strictOpts() Options { return Options{StrictNames: true} }

// Path traversal and absolute paths must be REFUSED, never repaired. A
// repaired ".." is still a component built from an escape attempt, and
// path.Join(root, "/etc/passwd") silently discards the root.
func TestAdversarial_TraversalAndAbsoluteAreRefused(t *testing.T) {
	for _, id := range []string{"adv-001", "adv-002"} {
		c := findCase(t, id)
		_, _, err := SanitizeRelPath(c.Input, strictOpts())
		if !errors.Is(err, ErrTraversal) {
			t.Errorf("%s: expected ErrTraversal, got %v\n  input: %q\n  why:   %s",
				id, err, c.Input, c.Why)
		}
	}
}

// A NUL terminates C strings. Go will happily carry it to the syscall,
// where behaviour is at best an error and at worst a silent truncation.
func TestAdversarial_NulByteIsRefused(t *testing.T) {
	c := findCase(t, "adv-003")
	if _, _, err := SanitizeRelPath(c.Input, strictOpts()); !errors.Is(err, ErrNulByte) {
		t.Errorf("expected ErrNulByte, got %v\n  why: %s", err, c.Why)
	}
}

// An embedded newline is legal on POSIX and corrupts every line-oriented
// log, shell pipeline and CSV export downstream.
func TestAdversarial_NewlineIsStripped(t *testing.T) {
	c := findCase(t, "adv-004")
	got, warns, err := SanitizeComponent(c.Input, strictOpts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("newline survived: %q", got)
	}
	if len(warns) == 0 {
		t.Error("a name that changed must produce a warning the user sees before execution")
	}
}

// Legal on ext4 (proven by #C14), illegal on SMB/NTFS/exFAT — which is
// where a large share of real libraries live.
func TestAdversarial_NonPOSIXCharsSanitisedInStrictMode(t *testing.T) {
	c := findCase(t, "adv-005")
	got, warns, err := SanitizeComponent(c.Input, strictOpts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got, `:"<>|?*\`) {
		t.Errorf("illegal SMB characters survived: %q", got)
	}
	if len(warns) == 0 {
		t.Error("expected a sanitisation warning")
	}

	// And in POSIX mode they must be LEFT ALONE: a film legitimately
	// called "Movie: The Sequel" keeps its colon on ext4.
	lenient, _, err := SanitizeComponent(c.Input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lenient, ":") {
		t.Errorf("non-strict mode must preserve POSIX-legal characters, got %q", lenient)
	}
}

// Reserved device stems are illegal on Windows and SMB with or without an
// extension.
func TestAdversarial_ReservedNamesAreEscaped(t *testing.T) {
	c := findCase(t, "adv-008")
	got, _, err := SanitizeComponent(c.Input, strictOpts())
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.ToLower(strings.SplitN(got, ".", 2)[0])
	if reservedStems[stem] {
		t.Errorf("reserved stem survived: %q", got)
	}
}

// Trailing spaces and dots are legal on POSIX and silently stripped by SMB,
// which creates a phantom path mismatch: the tool cannot find what it just
// wrote.
func TestAdversarial_TrailingSpaceIsTrimmed(t *testing.T) {
	c := findCase(t, "adv-009")
	got, _, err := SanitizeComponent(c.Input, strictOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimRight(got, ". ") {
		t.Errorf("trailing dot/space survived: %q", got)
	}
}

// NAME_MAX is 255 BYTES. Both of these exceed it once a suffix is added;
// the non-ASCII one exceeds it at 132 characters (#C13).
func TestAdversarial_LongNamesAreTruncatedWithinBudget(t *testing.T) {
	for _, id := range []string{"adv-006", "adv-007"} {
		c := findCase(t, id)
		ext := filepath.Ext(c.Input)
		stem := strings.TrimSuffix(c.Input, ext)

		got, warns := TruncateComponent(stem, ext, "", Options{})
		if len(got) > NameBudget {
			t.Errorf("%s: %d bytes exceeds the %d-byte budget", id, len(got), NameBudget)
		}
		if len(warns) == 0 {
			t.Errorf("%s: truncation must be recorded as a warning", id)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: truncation split a UTF-8 sequence: %q\n  why: %s", id, got, c.Why)
		}
		if !strings.HasSuffix(got, ext) {
			t.Errorf("%s: the extension must survive truncation, got %q", id, got)
		}
	}
}

// The budget must leave room for the partial suffix. A name truncated to
// exactly NAME_MAX fails ENAMETOOLONG on the copy path — which is the long
// non-ASCII title the rule exists to support.
func TestNameBudgetLeavesRoomForThePartialSuffix(t *testing.T) {
	if NameBudget+len(PartialSuffix) != 255 {
		t.Fatalf("budget %d + suffix %d != 255", NameBudget, len(PartialSuffix))
	}
	long := strings.Repeat("a", 400)
	got, _ := TruncateComponent(long, ".mkv", "", Options{})
	if len(got)+len(PartialSuffix) > 255 {
		t.Fatalf("truncated name plus partial suffix is %d bytes, over NAME_MAX",
			len(got)+len(PartialSuffix))
	}
}

// The structural marker is what a media server matches on. Truncating a long
// series title from the left would remove the episode code and leave a file
// nothing can place.
func TestTruncationProtectsTheStructuralMarker(t *testing.T) {
	long := strings.Repeat("Ы", 200) // 2 bytes per rune
	got, _ := TruncateComponent(long, ".mkv", " - S01E01", Options{})
	if !strings.Contains(got, "S01E01") {
		t.Fatalf("episode marker was truncated away: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation split a UTF-8 sequence: %q", got)
	}
	if len(got) > NameBudget {
		t.Fatalf("%d bytes exceeds budget %d", len(got), NameBudget)
	}
}

// Emoji are 4 UTF-8 bytes each and break naive character-count truncation.
// They are legal and must be preserved, not folded away.
func TestAdversarial_EmojiArePreserved(t *testing.T) {
	c := findCase(t, "adv-014")
	got, _, err := SanitizeComponent(c.Input, strictOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "🎬") {
		t.Errorf("emoji were stripped; sanitisation must not ASCII-fold: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
}

// Samples must never be imported as the feature. A 40 MB "sample.mkv" in the
// movie library is a support thread.
func TestSampleDetection(t *testing.T) {
	samples := []string{
		"Sample/sample.mkv",
		"sample.mkv",
		"Movie.2020.sample.mkv",
		"Extras/featurette.mkv",
	}
	for _, s := range samples {
		if !IsSampleOrExtra(s) {
			t.Errorf("IsSampleOrExtra(%q) = false, want true", s)
		}
	}
	// A film genuinely called "Sampler" must survive.
	keep := []string{"Sampler.2019.1080p.mkv", "The.Sample.Size.2020.mkv/feature.mkv"}
	for _, s := range keep {
		if IsSampleOrExtra(s) {
			t.Errorf("IsSampleOrExtra(%q) = true, want false", s)
		}
	}
}

func findCase(t *testing.T, id string) advCase {
	t.Helper()
	for _, c := range loadAdversarial(t) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("corpus case %s not found", id)
	return advCase{}
}
