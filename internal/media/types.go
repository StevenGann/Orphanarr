// Package media holds the value types shared across the pipeline.
//
// Nothing here performs I/O. The classifier and the layout engine are pure
// functions over these types (DESIGN §2.3), which is what lets the 118-case
// corpus run in milliseconds with no filesystem.
package media

import "fmt"

// Type is the media classification. The seven types come from BRIEF §2.3;
// Unknown is the eighth outcome the design insists on (DESIGN §4.6) and is
// never a failure — it is the honest answer.
type Type string

const (
	Movie     Type = "movie"
	TV        Type = "tv"
	Music     Type = "music"
	Ebook     Type = "ebook"
	Audiobook Type = "audiobook"
	Comic     Type = "comic"
	ROM       Type = "rom"
	Unknown   Type = "unknown"
)

// AllTypes is every classifiable type, excluding Unknown.
var AllTypes = []Type{Movie, TV, Music, Ebook, Audiobook, Comic, ROM}

func (t Type) Valid() bool {
	switch t {
	case Movie, TV, Music, Ebook, Audiobook, Comic, ROM, Unknown:
		return true
	}
	return false
}

// Cardinality answers "is this orphan one item or several?" (DESIGN §4.4).
// Multi routes to review under I9 — a season pack is one item, a folder of
// forty unrelated movies is not, and guessing wrong files forty things wrong.
type Cardinality string

const (
	Single Cardinality = "single"
	Multi  Cardinality = "multi"
	Mixed  Cardinality = "mixed"
)

// SourceFile is one file inside an orphan, as resolved from the client's
// manifest — never from a directory walk (I3).
type SourceFile struct {
	// RelPath is relative to the orphan root, using forward slashes.
	RelPath string
	// AbsPath is the container-local absolute path after path mapping.
	AbsPath string
	Size    int64
	// Ext is the lowercased extension including the dot, or "" if none.
	Ext string

	// Probe results, populated by inspect/probe on the second classification
	// pass only (DESIGN §2.3). Zero values mean "not probed".
	Probed       bool
	AudioTags    *AudioTags
	DurationSecs float64
}

// AudioTags is what probe reads from an audio container header. Music vs
// audiobook is the hardest live distinction in the design (§4.3) and these
// fields are why it is decidable at all.
type AudioTags struct {
	Artist      string
	AlbumArtist string
	Album       string
	Title       string
	Year        int
	TrackNo     int
	DiscNo      int
	Genre       string
	Compilation bool
	Narrator    string
	// HasChapters is the single strongest audiobook signal in a container
	// that also carries music tags.
	HasChapters bool
}

// FileSet is the classifier's entire input. It is fully materialized: the
// classifier never opens a file, so every corpus case can be expressed as
// one of these (DESIGN §2.3).
type FileSet struct {
	// Name is the orphan's own name — the torrent name or the root folder.
	Name string
	// Rootless is true when the torrent has no common root folder. Such
	// payloads route to review rather than being filed (D10).
	Rootless bool
	Files    []SourceFile
}

// TotalSize sums every file.
func (fs FileSet) TotalSize() int64 {
	var n int64
	for _, f := range fs.Files {
		n += f.Size
	}
	return n
}

// Largest returns the biggest file, or the zero value for an empty set.
func (fs FileSet) Largest() SourceFile {
	var best SourceFile
	for _, f := range fs.Files {
		if f.Size > best.Size {
			best = f
		}
	}
	return best
}

// CountExt returns how many files carry any of the given extensions.
func (fs FileSet) CountExt(exts ...string) int {
	set := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		set[e] = struct{}{}
	}
	n := 0
	for _, f := range fs.Files {
		if _, ok := set[f.Ext]; ok {
			n++
		}
	}
	return n
}

// Classification is the classifier's output, with the evidence that produced
// it. The evidence is persisted so that "why did it think that was a comic"
// is answerable after the fact (DESIGN §10.4).
type Classification struct {
	Type        Type
	Score       float64
	Cardinality Cardinality
	// Runner is the second-place type, used for the ambiguity margin (§4.5).
	Runner      Type
	RunnerScore float64
	Signals     []Signal
	// Reason is set when Type is Unknown, so the review queue can say why
	// rather than merely that.
	Reason string
}

// Signal is one piece of evidence and the weight it carried.
type Signal struct {
	Name   string
	Type   Type
	Weight float64
	Detail string
}

func (s Signal) String() string {
	return fmt.Sprintf("%s=%s(%.2f) %s", s.Name, s.Type, s.Weight, s.Detail)
}

// Parsed is what the release-name grammar recovered. Confidence is separate
// from the classification score: knowing a payload is TV is not the same as
// knowing which episode it is, and I9 gates on both (DESIGN §4.7).
type Parsed struct {
	Title      string
	Year       int
	Season     int
	Episode    int
	EpisodeEnd int
	// AirDate is set for date-based TV, which routes to review because the
	// date does not yield a season number (DESIGN §5.2).
	AirDate string
	// Absolute is set for anime absolute numbering, which also routes to
	// review — mapping it needs a lookup v1 does not have (§4.7).
	Absolute int

	Artist   string
	Album    string
	Series   string
	Volume   int
	Issue    string
	Author   string
	Narrator string
	Platform string
	Edition  string

	Confidence float64
}
