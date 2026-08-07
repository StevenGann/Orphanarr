// Package classify decides what media an orphan contains.
//
// Classify is a pure function: no I/O, no clock, no network (DESIGN §2.3).
// Everything it needs is materialised into the FileSet before it runs,
// including probe results, which is what lets the corpus run in
// milliseconds with no filesystem — and therefore what makes the corpus get
// run at all.
//
// The governing rule is DESIGN §4.6: when it cannot tell, it says so.
// Unknown is a first-class outcome, not a failure. A wrong confident answer
// is worse than an honest refusal, because the wrong answer gets filed.
package classify

import (
	"regexp"
	"strings"

	"github.com/StevenGann/Orphanarr/internal/classify/release"
	"github.com/StevenGann/Orphanarr/internal/media"
)

// Rules are the tunable thresholds. Defaults come from DESIGN §8.2.
type Rules struct {
	AutoThreshold   float64
	ReviewThreshold float64
	AmbiguityMargin float64
	// PDFDefault decides where a bare PDF goes: media.Unknown means review,
	// which is the shipped default (BRIEF Q23). Both Komga libraries are the
	// same server, so a misfile costs a shelf rather than readability — but
	// scanPdf is per-library, so it is not free.
	PDFDefault media.Type
}

func DefaultRules() Rules {
	return Rules{
		AutoThreshold:   0.85,
		ReviewThreshold: 0.50,
		AmbiguityMargin: 0.10,
		PDFDefault:      media.Unknown,
	}
}

var (
	reComicScene = regexp.MustCompile(`(?i)\((Digital|Web-DL|Scan|c2c)\)|Zone-Empire|Minutemen|GlorithHD`)
	// The negative lookahead is spelled out because Go's regexp has none:
	// a volume marker must not be followed by a dot-digit, or the version
	// token in "Star Wars ... Despecialized v2.7" reads as volume 2.
	reComicVolume = regexp.MustCompile(`(?i)\bv(?:ol)?\.?\s?(\d{1,3})([^\d.]|$)`)
	reComicChap   = regexp.MustCompile(`(?i)\bchapter\s*(\d{1,5})\b`)
	reIssueRange  = regexp.MustCompile(`\b(\d{1,4})\s*-\s*(\d{1,4})\b`)

	reAudioFormat = regexp.MustCompile(`(?i)[\[\(](FLAC|MP3|AAC|ALAC|WAV|OGG|APE|MP3-\d{3}|24-\d{2,3}|24BIT)[\]\)]`)
	// Scene music puts the format in a hyphen-delimited field rather than
	// in brackets: Artist-Album-WEB-FLAC-2011-GROUP.
	reAudioFormatBare = regexp.MustCompile(`(?i)(?:^|[-_. ])(FLAC|MP3|AAC|ALAC|APE|WAVPACK|OPUS|24BIT|\d{2}BIT|\d{2,3}KHZ|VINYL|CDDA|WEB-FLAC)(?:[-_. ]|$)`)
	reArtistAlbum     = regexp.MustCompile(`^(.+?)\s+-\s+(.+)$`)
	reVariousArt      = regexp.MustCompile(`(?i)^(VA|Various[\s_]Artists)\b`)
	reDiscography     = regexp.MustCompile(`(?i)\b(discography|complete[\s._-]*discography|anthology[\s._-]*box)\b`)

	reAudiobookWord = regexp.MustCompile(`(?i)\b(audiobook|unabridged|abridged|narrated[\s._-]*by)\b`)
	reNarratorBrace = regexp.MustCompile(`\{([^}]+)\}`)
	// {imdb-tt…}, {tmdb-…}, {edition-…} are Plex metadata tags, not people.
	reMetadataBrace = regexp.MustCompile(`(?i)^(imdb|tmdb|tvdb|anidb|edition|tvdbid)-`)
	reNarratorParen = regexp.MustCompile(`\(([A-Z][a-z]+(?:\.?\s+[A-Z]\.?)*\s+[A-Z][a-z]+)\)`)
	rePartNumbering = regexp.MustCompile(`(?i)\b(part|track|chapter|disc|disk|cd)[\s._-]*(\d{1,4})\b`)

	reROMRegion = regexp.MustCompile(`\((USA|Europe|Japan|World|USA,\s*Europe|Japan,\s*USA|Australia|Korea|Brazil|Spain|France|Germany|Italy|China|Taiwan|UEP)[^)]*\)`)
	// GoodTools/No-Intro bracket tags are short and structured: [!], [a1],
	// [b2], [h1C], [T+Eng1.0_RPGOne]. An earlier version matched any
	// bracket beginning with one of those letters, which claimed
	// "[BluRay]" and "[BitTorrent]" — every YTS movie and every junk
	// torrent in the completed directory.
	reROMTags = regexp.MustCompile(`\((Rev\s*\d+|Proto|Beta|Sample|Unl|SGB Enhanced|GB Compatible)\)|\[(!|[abfho]\d{1,2}[A-Za-z]?|[Tt][+-][A-Za-z0-9._]+)\]`)
	reBIOS    = regexp.MustCompile(`(?i)^(scph|dc_boot|bios|gba_bios|lynxboot|syscard)`)
	reRomset  = regexp.MustCompile(`(?i)\b(No-Intro|Redump|TOSEC|GoodSet|MAME)\b`)

	reGenericName = regexp.MustCompile(`(?i)^(untitled|new folder|scans|flac|mp3|downloads?|temp|misc|stuff|files?|movies?|music|books?|games?|roms?|season\s*\d+|cd\d+|disc\s*\d+)$`)
)

// Classify is the whole decision. It runs at most twice per orphan: once
// over the cheap FileSet and, if the first pass lands below the auto
// threshold or the payload is audio, once more over a FileSet enriched with
// probe results (DESIGN §2.3). Classify itself never opens a file.
func Classify(fs media.FileSet, r Rules) (media.Classification, media.Parsed) {
	c := media.Classification{Cardinality: media.Single}
	scores := map[media.Type]float64{}
	add := func(name string, t media.Type, w float64, detail string) {
		if t == media.Unknown {
			return
		}
		scores[t] += w
		c.Signals = append(c.Signals, media.Signal{Name: name, Type: t, Weight: w, Detail: detail})
	}

	name := displayName(fs)
	info := release.Parse(stripKnownExt(name))
	parsed := toParsed(info)

	counts := countFamilies(fs)

	// A payload that is only an archive set has no visible media type at
	// all. Extraction is Unpackerr's job (DESIGN §1.2), so this parks
	// rather than guesses.
	if counts[famArchive] > 0 && primaryCount(counts) == 0 {
		c.Type = media.Unknown
		c.Reason = "needs_extraction"
		c.Signals = append(c.Signals, media.Signal{
			Name: "archive-only", Type: media.Unknown, Weight: 1,
			Detail: "payload is a .rar/.7z set; the media type is invisible until extraction",
		})
		return c, parsed
	}

	// Mixed content: more than one primary family with real presence. No
	// single destination is correct, so it is surfaced rather than split —
	// splitting has its own rollback story (DESIGN §1.2).
	if fams, mixed := detectMixed(counts); mixed {
		c.Type = media.Unknown
		c.Cardinality = media.Mixed
		c.Reason = "multi-type"
		c.Signals = append(c.Signals, media.Signal{
			Name: "mixed", Type: media.Unknown, Weight: 1,
			Detail: "distinct media families present: " + strings.Join(fams, ", "),
		})
		return c, parsed
	}

	extensionSignals(fs, counts, r, add)
	nameSignals(name, info, counts, add)
	structureSignals(fs, counts, info, add)

	// A generic container name with nothing else to go on is the case the
	// corpus is most insistent about: "Untitled", "Scans", "FLAC",
	// "Season 1". Inventing a signal here is how files get lost.
	if reGenericName.MatchString(strings.TrimSpace(name)) && primaryCount(counts) == 0 {
		c.Type = media.Unknown
		c.Reason = "no_signal"
		c.Signals = append(c.Signals, media.Signal{
			Name: "generic-name", Type: media.Unknown, Weight: 1,
			Detail: "container name carries no media signal and there are no files to read",
		})
		return c, parsed
	}

	best, bestScore, runner, runnerScore := top2(scores)
	c.Type, c.Score = best, bestScore
	c.Runner, c.RunnerScore = runner, runnerScore
	c.Cardinality = cardinality(fs, counts, info)

	// Type-specific fields are recovered only after the type is known,
	// because the same token means different things per type. Without this
	// the layout engine receives eight zero-valued fields and files five of
	// the seven media types to the wrong place.
	enrich(&parsed, c.Type, fs, info)

	switch {
	case best == media.Unknown || bestScore < r.ReviewThreshold:
		c.Type = media.Unknown
		if c.Reason == "" {
			c.Reason = "below_review_threshold"
		}
	// The epsilon is not decoration. Scores are sums of float64 literals,
	// so a genuine 0.90 vs 0.80 gap computes as 0.09999999999999998 and a
	// naive "< 0.10" sends a correctly-classified audiobook to review.
	case bestScore-runnerScore < r.AmbiguityMargin-1e-9 && runner != media.Unknown && runnerScore > 0:
		// Two plausible answers within the margin. §4.5 says tie-break
		// explicitly or refuse; refusing is the honest option.
		c.Type = media.Unknown
		c.Reason = "ambiguous:" + string(best) + "/" + string(runner)
	}

	return c, parsed
}

// displayName picks the string that names the orphan.
func displayName(fs media.FileSet) string {
	if fs.Name != "" {
		return fs.Name
	}
	if len(fs.Files) == 1 {
		return fs.Files[0].RelPath
	}
	return ""
}

func countFamilies(fs media.FileSet) map[family]int {
	out := map[family]int{}
	for _, f := range fs.Files {
		out[familyOf(f.Ext)]++
	}
	return out
}

func primaryCount(counts map[family]int) int {
	n := 0
	for f, c := range counts {
		if f.primary() {
			n += c
		}
	}
	return n
}

// detectMixed reports whether several primary families are meaningfully
// present. The 30% qualifier exists because a bonus video inside an album
// (one .mkv among a dozen .flac) is an album, not mixed content — and the
// corpus says so.
func detectMixed(counts map[family]int) ([]string, bool) {
	total := primaryCount(counts)
	if total == 0 {
		return nil, false
	}
	var present []string
	significant := 0
	for _, f := range []family{famVideo, famAudio, famBook, famComic, famROM, famPDF} {
		n := counts[f]
		if n == 0 {
			continue
		}
		present = append(present, familyName(f))
		// A family counts as significant only with real presence: at least
		// two files and at least 30% of the payload. One stray file is a
		// bonus feature, not a second library.
		if n >= 2 && float64(n)/float64(total) >= 0.30 {
			significant++
		}
	}

	// Three or more distinct primary families is mixed content regardless
	// of how few files each has. One video, one album track and one ebook
	// is three libraries and no correct destination — whereas two families
	// with a clear majority (an album with a bonus concert video) is an
	// album, which is why that case needs the stricter test above.
	if len(present) >= 3 {
		return present, true
	}
	return present, significant >= 2
}

func familyName(f family) string {
	switch f {
	case famVideo:
		return "video"
	case famAudio:
		return "audio"
	case famBook:
		return "book"
	case famComic:
		return "comic"
	case famROM:
		return "rom"
	case famPDF:
		return "pdf"
	}
	return "other"
}

func top2(scores map[media.Type]float64) (media.Type, float64, media.Type, float64) {
	best, runner := media.Unknown, media.Unknown
	var bestS, runnerS float64
	// Iterate a fixed order so ties resolve deterministically rather than by
	// Go's randomised map order — a classifier that answers differently on
	// alternate runs is untestable.
	for _, t := range media.AllTypes {
		s := scores[t]
		if s > bestS {
			runner, runnerS = best, bestS
			best, bestS = t, s
		} else if s > runnerS {
			runner, runnerS = t, s
		}
	}
	return best, bestS, runner, runnerS
}

func toParsed(i release.Info) media.Parsed {
	p := media.Parsed{
		Title:      i.Title,
		Year:       i.Year,
		Season:     i.Season,
		AirDate:    i.AirDate,
		Absolute:   i.Absolute,
		Edition:    i.Edition,
		Confidence: i.Confidence,
	}
	if len(i.Episodes) > 0 {
		p.Episode = i.Episodes[0]
		p.EpisodeEnd = i.Episodes[len(i.Episodes)-1]
	}
	return p
}

// stripKnownExt removes a recognised container extension.
//
// It looks the extension up rather than iterating a slice built from
// extFamily at package-variable initialisation time. That earlier version
// was silently a no-op: Go initialises package variables before running
// init(), so the slice was built from an empty map and every name kept its
// extension — which made ".mp3" match the scene audio-format field and
// classified three audiobooks as music.
func stripKnownExt(name string) string {
	base := name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	i := strings.LastIndexByte(base, '.')
	if i <= 0 {
		return name
	}
	ext := strings.ToLower(base[i:])
	if _, known := extFamily[ext]; !known {
		return name
	}
	return name[:len(name)-len(ext)]
}
