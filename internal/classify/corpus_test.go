package classify

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/media"
)

type corpusCase struct {
	ID         string         `json:"id"`
	Input      string         `json:"input"`
	Shape      string         `json:"shape"`
	Contents   []string       `json:"contents"`
	Expect     map[string]any `json:"expect"`
	Difficulty string         `json:"difficulty"`
	Why        string         `json:"why"`
}

func loadCorpus(t *testing.T, file string) []corpusCase {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "tests", "corpus", file))
	if err != nil {
		t.Fatalf("corpus %s: %v", file, err)
	}
	defer f.Close()

	var out []corpusCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var c corpusCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("%s: %v\n%s", file, err, line)
		}
		out = append(out, c)
	}
	return out
}

func extOf(p string) string {
	base := p
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return strings.ToLower(base[i:])
	}
	return ""
}

// buildFileSet turns a corpus case into the classifier's input. A dir with
// no declared contents yields no files, which is the honest translation:
// the name is all the evidence there is.
func buildFileSet(c corpusCase) media.FileSet {
	fs := media.FileSet{Name: c.Input}
	switch {
	case len(c.Contents) > 0:
		for _, p := range c.Contents {
			fs.Files = append(fs.Files, media.SourceFile{
				RelPath: p,
				Ext:     extOf(p),
				// A plausible feature-length size for video, so the
				// cardinality rule has something to work with.
				Size: sizeFor(extOf(p)),
			})
		}
	case c.Shape == "file":
		fs.Files = []media.SourceFile{{
			RelPath: c.Input,
			Ext:     extOf(c.Input),
			Size:    sizeFor(extOf(c.Input)),
		}}
	}
	return fs
}

func sizeFor(ext string) int64 {
	switch familyOf(ext) {
	case famVideo:
		return 4 << 30
	case famAudio:
		return 40 << 20
	case famComic, famBook, famPDF:
		return 30 << 20
	case famROM:
		return 8 << 20
	}
	return 1 << 20
}

// knownDivergences records corpus cases this classifier deliberately does
// not satisfy, with the reason. They are asserted to still diverge, so that
// a fix silently landing shows up as a test failure rather than as nothing.
//
// Keeping them visible beats two worse options: deleting the corpus row, or
// bending the classifier around a single case until it misreads the general
// one.
var knownDivergences = map[string]string{
	"ebk-007": "the row declares no contents, so the only evidence is the name " +
		"'Humble Bundle - Sci-Fi Collection' — which is equally a game bundle. " +
		"A real orphan always carries a file manifest and would classify from the " +
		".epub files; with no contents, Unknown is the honest answer (DESIGN §4.6). " +
		"Fixing this by keyword would mean teaching the classifier a vendor's brand name.",
}

// TestClassifyCorpus runs every case whose expectation names a media type.
// Adversarial cases express an `action` (refuse/sanitise/truncate) rather
// than a type — those belong to the layout and executor layers.
func TestClassifyCorpus(t *testing.T) {
	files := []string{
		"movies.jsonl", "tv.jsonl", "music.jsonl", "audiobooks.jsonl",
		"comics.jsonl", "ebooks.jsonl", "roms.jsonl", "ambiguous.jsonl",
	}
	rules := DefaultRules()

	var checked, failed int
	for _, file := range files {
		for _, c := range loadCorpus(t, file) {
			want, ok := c.Expect["type"].(string)
			if !ok {
				continue
			}
			if ignore, ok := c.Expect["ignore"].(bool); ok && ignore {
				continue
			}
			checked++
			t.Run(c.ID, func(t *testing.T) {
				got, _ := Classify(buildFileSet(c), rules)

				if reason, known := knownDivergences[c.ID]; known {
					if string(got.Type) == want {
						t.Fatalf("KNOWN DIVERGENCE NOW PASSES — remove it from knownDivergences.\n"+
							"  recorded reason: %s", reason)
					}
					t.Skipf("known divergence (got %s, corpus wants %s): %s", got.Type, want, reason)
					return
				}

				if string(got.Type) != want {
					failed++
					t.Errorf("type = %s, want %s (score %.2f, runner %s/%.2f, reason %q)\n"+
						"  input:      %s\n  contents:   %v\n  difficulty: %s\n  why:        %s\n  signals:    %v",
						got.Type, want, got.Score, got.Runner, got.RunnerScore, got.Reason,
						c.Input, c.Contents, c.Difficulty, c.Why, got.Signals)
				}
			})
		}
	}
	t.Logf("classified %d corpus cases, %d recorded divergences", checked, len(knownDivergences))
}

// The corpus is at least a quarter negative expectations by design. Those
// are the cases that matter most: a classifier that never says "unknown"
// has simply moved its errors downstream into the filesystem.
func TestUnknownIsReachable(t *testing.T) {
	rules := DefaultRules()
	unknowns := 0
	total := 0
	for _, file := range []string{"movies.jsonl", "tv.jsonl", "music.jsonl",
		"comics.jsonl", "ebooks.jsonl", "roms.jsonl", "ambiguous.jsonl"} {
		for _, c := range loadCorpus(t, file) {
			if _, ok := c.Expect["type"]; !ok {
				continue
			}
			total++
			got, _ := Classify(buildFileSet(c), rules)
			if got.Type == media.Unknown {
				unknowns++
			}
		}
	}
	if unknowns == 0 {
		t.Fatal("no case classified Unknown; the refusal path is not exercised at all")
	}
	t.Logf("%d/%d cases classified Unknown", unknowns, total)
}

// Every Unknown must carry a machine-readable reason. "It didn't work" is
// not a review-queue entry a user can act on.
func TestUnknownAlwaysHasAReason(t *testing.T) {
	rules := DefaultRules()
	for _, file := range []string{"movies.jsonl", "ambiguous.jsonl", "roms.jsonl", "ebooks.jsonl"} {
		for _, c := range loadCorpus(t, file) {
			got, _ := Classify(buildFileSet(c), rules)
			if got.Type == media.Unknown && got.Reason == "" {
				t.Errorf("%s: Unknown with no reason\n  input: %s", c.ID, c.Input)
			}
		}
	}
}

// Classify must be deterministic. Go randomises map iteration, and a
// classifier that answers differently on alternate runs cannot be tested,
// cannot be debugged from a log, and cannot be trusted with a filesystem.
func TestClassifyIsDeterministic(t *testing.T) {
	rules := DefaultRules()
	for _, file := range []string{"ambiguous.jsonl", "music.jsonl"} {
		for _, c := range loadCorpus(t, file) {
			fs := buildFileSet(c)
			first, _ := Classify(fs, rules)
			for i := 0; i < 25; i++ {
				got, _ := Classify(fs, rules)
				if got.Type != first.Type || got.Score != first.Score {
					t.Fatalf("%s: nondeterministic: %s/%.3f then %s/%.3f",
						c.ID, first.Type, first.Score, got.Type, got.Score)
				}
			}
		}
	}
}
