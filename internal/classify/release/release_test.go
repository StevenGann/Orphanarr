package release

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corpusCase mirrors the JSONL contract documented in tests/corpus/README.md.
type corpusCase struct {
	ID         string          `json:"id"`
	Input      string          `json:"input"`
	Shape      string          `json:"shape"`
	Contents   []string        `json:"contents"`
	Expect     map[string]any  `json:"expect"`
	Difficulty string          `json:"difficulty"`
	Why        string          `json:"why"`
	Raw        json.RawMessage `json:"-"`
}

func loadCorpus(t *testing.T, file string) []corpusCase {
	t.Helper()
	path := filepath.Join("..", "..", "..", "tests", "corpus", file)
	f, err := os.Open(path)
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
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func expectString(c corpusCase, key string) (string, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func expectInt(c corpusCase, key string) (int, bool) {
	v, ok := c.Expect[key]
	if !ok || v == nil {
		return 0, false
	}
	f, ok := v.(float64)
	return int(f), ok
}

// stripExt removes a container extension so the parser sees the same string
// for "X.mkv" and the directory "X".
func stripExt(name string) string {
	base := name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	known := []string{".mkv", ".mp4", ".avi", ".m4v", ".ts", ".flac", ".mp3", ".m4b",
		".epub", ".azw3", ".mobi", ".pdf", ".cbz", ".cbr", ".sfc", ".smc", ".z64",
		".bin", ".cue", ".gb", ".md", ".nes", ".iso", ".zip"}
	lower := strings.ToLower(base)
	for _, e := range known {
		if strings.HasSuffix(lower, e) {
			return name[:len(name)-len(e)]
		}
	}
	return name
}

// TestMovieCorpus checks the title and year the movie corpus specifies.
// Cases whose expectation is "unknown" belong to the classifier, not the
// parser, and are skipped here.
func TestMovieCorpus(t *testing.T) {
	for _, c := range loadCorpus(t, "movies.jsonl") {
		if ty, _ := expectString(c, "type"); ty != "movie" {
			continue
		}
		if ignore, ok := c.Expect["ignore"].(bool); ok && ignore {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			got := Parse(stripExt(c.Input))

			if want, ok := expectString(c, "title"); ok {
				if got.Title != want {
					t.Errorf("title = %q, want %q\n  input: %s\n  why:   %s",
						got.Title, want, c.Input, c.Why)
				}
			}
			if want, ok := expectInt(c, "year"); ok {
				if got.Year != want {
					t.Errorf("year = %d, want %d\n  input: %s\n  why:   %s",
						got.Year, want, c.Input, c.Why)
				}
			}
			if want, ok := expectString(c, "edition"); ok {
				if got.Edition != want {
					t.Errorf("edition = %q, want %q\n  why: %s", got.Edition, want, c.Why)
				}
			}
			if want, ok := expectString(c, "imdb"); ok && got.IMDB != want {
				t.Errorf("imdb = %q, want %q", got.IMDB, want)
			}
			if got.IsTV {
				t.Errorf("parsed as TV; a film must not carry episode fields\n  why: %s", c.Why)
			}
		})
	}
}

func TestTVCorpus(t *testing.T) {
	for _, c := range loadCorpus(t, "tv.jsonl") {
		if ty, _ := expectString(c, "type"); ty != "tv" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			got := Parse(stripExt(c.Input))

			if !got.IsTV {
				t.Fatalf("not recognised as TV\n  input: %s\n  why:   %s", c.Input, c.Why)
			}
			if want, ok := expectString(c, "series"); ok {
				if got.Title != want {
					t.Errorf("series = %q, want %q\n  input: %s\n  why:   %s",
						got.Title, want, c.Input, c.Why)
				}
			}
			if want, ok := expectInt(c, "season"); ok {
				if got.Season != want {
					t.Errorf("season = %d, want %d\n  why: %s", got.Season, want, c.Why)
				}
			}
			if want, ok := expectInt(c, "episode"); ok {
				if len(got.Episodes) == 0 || got.Episodes[0] != want {
					t.Errorf("episode = %v, want %d\n  why: %s", got.Episodes, want, c.Why)
				}
			}
			if raw, ok := c.Expect["episodes"]; ok {
				arr, _ := raw.([]any)
				if len(arr) != len(got.Episodes) {
					t.Errorf("episodes = %v, want %v\n  why: %s", got.Episodes, arr, c.Why)
				} else {
					for i, v := range arr {
						if got.Episodes[i] != int(v.(float64)) {
							t.Errorf("episodes = %v, want %v", got.Episodes, arr)
							break
						}
					}
				}
			}
			if want, ok := expectString(c, "air_date"); ok && got.AirDate != want {
				t.Errorf("air_date = %q, want %q\n  why: %s", got.AirDate, want, c.Why)
			}
			if want, ok := expectInt(c, "absolute_episode"); ok && got.Absolute != want {
				t.Errorf("absolute = %d, want %d\n  why: %s", got.Absolute, want, c.Why)
			}
		})
	}
}

// The parser must never report high confidence for a name that carries no
// information. I9 gates on this value, so an inflated score here is what
// turns "I don't know" into a file in the wrong place.
func TestConfidenceIsLowForNamelessInput(t *testing.T) {
	for _, name := range []string{"movie", "Untitled", "Season 1", "FLAC", "Scans", "game"} {
		got := Parse(name)
		if got.Confidence > 0.5 {
			t.Errorf("Parse(%q).Confidence = %.2f; a name with no release metadata must not be confident",
				name, got.Confidence)
		}
	}
}

// Date-based TV and anime absolute numbering must stay below the 0.85 auto
// threshold: neither yields a season number, and guessing one files the
// episode into a season that does not exist (DESIGN §4.7, §5.2).
func TestUnmappableTVStaysBelowAutoThreshold(t *testing.T) {
	cases := []string{
		"The.Daily.Show.2024.03.14.Guest.Name.1080p.WEB.h264-GRP",
		"[SubsPlease] Frieren - 12 (1080p) [A1B2C3D4]",
	}
	for _, name := range cases {
		got := Parse(name)
		if got.Confidence >= 0.85 {
			t.Errorf("Parse(%q).Confidence = %.2f; must stay below the auto threshold",
				name, got.Confidence)
		}
	}
}
