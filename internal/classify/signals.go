package classify

import (
	"regexp"
	"strings"

	"github.com/StevenGann/Orphanarr/internal/classify/release"
	"github.com/StevenGann/Orphanarr/internal/media"
)

type addFn func(name string, t media.Type, w float64, detail string)

var (
	reTrackPrefix = regexp.MustCompile(`^\d{1,3}\s*[-._]\s*\S`)
	reBookEdition = regexp.MustCompile(`(?i)\b\d+(st|nd|rd|th)\s+edition\b`)
)

// extensionSignals reads what the files themselves are. Extensions are the
// cheapest evidence and, for a few formats, near-decisive: nothing but a
// comic is shipped as .cbz, and nothing but an audiobook as .m4b.
func extensionSignals(fs media.FileSet, counts map[family]int, r Rules, add addFn) {
	if counts[famComic] > 0 {
		add("ext-comic", media.Comic, 0.90, "comic archive present")
	}
	if counts[famBook] > 0 {
		add("ext-book", media.Ebook, 0.90, "reflowable ebook format present")
	}
	if n := countExt(fs, ".m4b"); n > 0 {
		// .m4b is the audiobook container. Audiobookshelf treats it as
		// native and nothing else ships in it.
		add("ext-m4b", media.Audiobook, 0.90, "m4b container present")
	}
	if counts[famROM] > 0 {
		plat := ""
		for _, f := range fs.Files {
			if p, ok := romPlatform[f.Ext]; ok {
				plat = p
				break
			}
		}
		add("ext-rom", media.ROM, 0.85, "extension implies exactly one platform: "+plat)
	}

	// A bare PDF is genuinely undecidable: Komga serves it as a book with
	// its own media profile, and a reflowable reader claims it too. The
	// corpus has this three ways — a volume marker makes it a comic, an
	// edition marker makes it a book, and nothing at all makes it neither.
	if counts[famPDF] > 0 && counts[famComic] == 0 && counts[famBook] == 0 {
		// Strip the extension first: "…Vol 1.pdf" ends in ".pdf", and the
		// volume matcher refuses a digit followed by a dot so that it does
		// not read a version token like "v2.7" as volume 2.
		name := stripKnownExt(displayName(fs))
		switch {
		case reComicVolume.MatchString(name) || reComicChap.MatchString(name) || reComicScene.MatchString(name):
			add("pdf-comic-marker", media.Comic, 0.80, "PDF with a volume/chapter marker")
		case reBookEdition.MatchString(name):
			add("pdf-book-marker", media.Ebook, 0.80, "PDF with an edition marker")
		case r.PDFDefault != media.Unknown:
			add("pdf-default", r.PDFDefault, 0.60, "configured pdf_default")
		default:
			// Deliberately no signal. Falling through to Unknown is the
			// shipped behaviour (BRIEF Q23).
		}
	}
}

// nameSignals reads the orphan's own name. This is the only evidence
// available when the payload is a directory whose contents we have not been
// given, which is most of the corpus.
func nameSignals(name string, info release.Info, counts map[family]int, add addFn) {
	base := stripKnownExt(name)

	// Television. An episode code dominates media-type choice even when the
	// title looks like a film — "Blade.Runner.2049.S01E01" is television.
	if info.IsTV && strings.TrimSpace(info.Title) != "" {
		switch {
		case info.AirDate != "":
			add("name-tv-date", media.TV, 0.90, "date-based episode "+info.AirDate)
		case info.Absolute > 0:
			add("name-tv-absolute", media.TV, 0.90, "anime absolute numbering")
		default:
			add("name-tv", media.TV, 0.95, "season/episode code")
		}
	}

	// Audiobook markers beat everything else that shares the audio family:
	// "Dune.2019.Audiobook.MP3" collides head-on with a film of the same
	// name, and the word is the only thing separating them.
	if reAudiobookWord.MatchString(base) {
		add("name-audiobook", media.Audiobook, 0.90, "explicit audiobook marker")
	}
	if m := reNarratorBrace.FindStringSubmatch(base); m != nil && counts[famComic] == 0 {
		// {imdb-tt0499549} is a Plex metadata tag, not a narrator. Reading
		// it as one turns every already-organised film into an audiobook.
		if !reMetadataBrace.MatchString(m[1]) {
			add("name-narrator", media.Audiobook, 0.85, "narrator in braces: "+m[1])
		}
	}

	// Music.
	hasFormatTag := false
	if m := reAudioFormat.FindStringSubmatch(base); m != nil {
		hasFormatTag = true
		add("name-audio-format", media.Music, 0.85, "audio format tag: "+m[1])
	} else if m := reAudioFormatBare.FindStringSubmatch(base); m != nil {
		hasFormatTag = true
		add("name-audio-format-scene", media.Music, 0.80, "scene audio format field: "+m[1])
	}
	if reVariousArt.MatchString(base) {
		add("name-various-artists", media.Music, 0.85, "VA/Various Artists compilation")
	}
	if reDiscography.MatchString(base) {
		add("name-discography", media.Music, 0.85, "discography marker")
	}
	// "Artist - Album (Year)" is a music convention strong enough to
	// outrank the bare year that would otherwise make this a film. Films
	// are not usually named "X - Y (YYYY)"; albums almost always are.
	if !info.IsTV && info.Year > 0 && reArtistAlbum.MatchString(base) &&
		counts[famVideo] == 0 && counts[famComic] == 0 && counts[famBook] == 0 {
		add("name-artist-album", media.Music, 0.70, "'Artist - Album (Year)' form")
	}
	// A narrator in parentheses rather than braces is common in
	// hand-organised audiobook folders. It is weaker than the brace form
	// because parentheses carry everything else too, so any format tag or
	// comic marker outranks it.
	if !hasFormatTag && counts[famComic] == 0 && counts[famBook] == 0 {
		// Weighted the same as the brace form. A personal name in
		// parentheses beside a book title is a narrator credit, and the
		// year gate above is what keeps it away from "Album (2014)".
		if m := reNarratorParen.FindStringSubmatch(base); m != nil && info.Year == 0 {
			add("name-narrator-paren", media.Audiobook, 0.85, "narrator in parentheses: "+m[1])
		}
	}

	// Comics.
	if reComicScene.MatchString(base) {
		add("name-comic-scene", media.Comic, 0.85, "comic scene tag")
	}
	if counts[famVideo] == 0 && counts[famAudio] == 0 && !hasFormatTag {
		if m := reComicVolume.FindStringSubmatch(base); m != nil {
			add("name-comic-volume", media.Comic, 0.70, "volume marker v"+m[1])
		}
		if m := reComicChap.FindStringSubmatch(base); m != nil {
			add("name-comic-chapter", media.Comic, 0.85, "chapter marker "+m[1])
		}
		// An issue range must look like issue numbers. "1 - 2010" in an
		// Audiobookshelf path and "24-192" in a sample-rate tag are both
		// two numbers with a hyphen, and neither is a comic.
		// No year gate here: "Amazing Spider-Man 001-050 (1963-1967)" has
		// both an issue range and a year range, and the parser reads the
		// latter as a year.
		//
		// Two guards instead. The span must be plausible for issue
		// numbers, and at least one side must be zero-padded to three
		// digits — which real issue ranges are and incidental number pairs
		// are not. Without the padding rule, "Selected Ambient Works
		// 85-92" is a fifty-issue comic run.
		if m := reIssueRange.FindStringSubmatch(base); m != nil {
			lo, hi := atoi(m[1]), atoi(m[2])
			padded := len(m[1]) >= 3 || len(m[2]) >= 3
			if padded && lo >= 0 && hi > lo && hi-lo < 500 {
				add("name-comic-range", media.Comic, 0.80, "issue range "+m[1]+"-"+m[2])
			}
		}
	}

	// ROMs. A No-Intro/Redump region tag is the strongest available signal
	// for a console image, and it is what rescues the extensions that are
	// not decisive on their own (.iso, .bin, .md).
	if m := reROMRegion.FindStringSubmatch(base); m != nil {
		add("name-rom-region", media.ROM, 0.85, "region tag "+m[1])
	}
	if reRomset.MatchString(base) {
		add("name-romset", media.ROM, 0.85, "romset naming convention")
	}
	if reBIOS.MatchString(strings.ToLower(lastComponent(base))) {
		add("name-bios", media.ROM, 0.85, "BIOS image naming")
	}
	if reROMTags.MatchString(base) && counts[famVideo] == 0 {
		add("name-rom-tags", media.ROM, 0.55, "GoodTools/No-Intro tag")
	}

	// Films. A year with no episode code is weak on its own — it is what
	// separates a film from a nameless blob, not what proves it is a film —
	// so it sits just above the review threshold and any stronger signal
	// overrides it.
	if !info.IsTV && info.Year > 0 && strings.TrimSpace(info.Title) != "" {
		add("name-year", media.Movie, 0.55, "year token with no episode marker")
	}
}

// structureSignals read the shape of the payload: how many files, of what,
// in what proportion. Names lie; contents do not.
func structureSignals(fs media.FileSet, counts map[family]int, info release.Info, add addFn) {
	total := primaryCount(counts)
	if total == 0 {
		return
	}

	video, audio := counts[famVideo], counts[famAudio]

	// A dominant video payload with a year and no episode code is a film.
	// Requiring the year is what keeps "Concert.mkv" out of the movie
	// library: a lone video with no release metadata is not evidence.
	if video > 0 && float64(video)/float64(total) >= 0.5 {
		switch {
		case info.IsTV:
			add("struct-video-tv", media.TV, 0.35, "video payload with an episode marker")
		case info.Year > 0:
			add("struct-video-movie", media.Movie, 0.35, "video payload with a year")
		}
	}

	if audio > 0 && float64(audio)/float64(total) >= 0.5 {
		// Audio dominance is counted by FILE COUNT, not by bytes. A bonus
		// concert video inside an album is larger than every track put
		// together, so majority-by-bytes routes the album to Plex — which
		// is the corpus case this rule exists for.
		add("struct-audio", media.Music, 0.45, "audio-dominant payload")

		parts := 0
		for _, f := range fs.Files {
			if familyOf(f.Ext) == famAudio && rePartNumbering.MatchString(f.RelPath) {
				parts++
			}
		}
		// Every audio file numbered as a part, chapter or disc — rather
		// than as a track — is the shape of a book, not an album. The
		// whole path counts, because Audiobookshelf's Disc subfolders put
		// the marker in a parent directory rather than the filename.
		if parts > 0 && parts == audio {
			add("struct-audio-parts", media.Audiobook, 0.70,
				"audio files numbered as parts/chapters/discs, not tracks")
		}

		// A single audio file that is not track-numbered is more likely a
		// book than a single. "Neil Gaiman - American Gods.mp3" and
		// "01 - Paranoid Android.flac" are the same syntax with different
		// meanings, and the track number is the whole difference.
		if audio == 1 && parts == 0 {
			only := firstOfFamily(fs, famAudio)
			nameOnly := lastComponent(stripKnownExt(only.RelPath))
			if reTrackPrefix.MatchString(nameOnly) {
				// Weak on purpose. Audiobook chapters are numbered exactly
				// like album tracks, so this must not outrank a narrator
				// credit in the parent folder.
				add("struct-track-number", media.Music, 0.25, "track-numbered filename")
			} else if reArtistAlbum.MatchString(nameOnly) {
				add("struct-single-audio", media.Audiobook, 0.60,
					"lone untracked audio file in 'Name - Title' form")
			}
		}
	}

	// A book beside an OPF sidecar is a library export, not a stray file.
	if counts[famBook] > 0 && countExt(fs, ".opf") > 0 {
		add("struct-opf", media.Ebook, 0.35, "OPF metadata sidecar present")
	}
}

// cardinality answers "is this one item or several?" (DESIGN §4.4). Multi
// routes to review under I9, because filing a folder of forty unrelated
// films as one film files forty things wrong.
func cardinality(fs media.FileSet, counts map[family]int, info release.Info) media.Cardinality {
	if info.SeriesPack {
		return media.Multi
	}
	if reDiscography.MatchString(displayName(fs)) {
		return media.Multi
	}
	// Several self-contained books or comics in one payload have no single
	// destination folder.
	if counts[famBook] >= 3 || counts[famComic] >= 3 {
		return media.Multi
	}
	// Several feature-length videos with no episode marker: a pack of
	// films, not one film with extras.
	if !info.IsTV && countLargeVideos(fs) >= 3 {
		return media.Multi
	}
	return media.Single
}

const featureBytes = 300 << 20 // 300 MiB, per DESIGN §4.2's movie rule

func countLargeVideos(fs media.FileSet) int {
	n := 0
	for _, f := range fs.Files {
		if familyOf(f.Ext) == famVideo && f.Size >= featureBytes {
			n++
		}
	}
	return n
}

func countExt(fs media.FileSet, ext string) int {
	n := 0
	for _, f := range fs.Files {
		if f.Ext == ext {
			n++
		}
	}
	return n
}

func firstOfFamily(fs media.FileSet, fam family) media.SourceFile {
	for _, f := range fs.Files {
		if familyOf(f.Ext) == fam {
			return f
		}
	}
	return media.SourceFile{}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func lastComponent(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
