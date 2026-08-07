package classify

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/StevenGann/Orphanarr/internal/classify/release"
	"github.com/StevenGann/Orphanarr/internal/media"
)

// Type-specific parsing.
//
// The release grammar in classify/release recovers what a scene name carries:
// title, year, season, episode. That is enough for films and television and
// nothing else. Music needs an artist and an album, comics a series and a
// volume, audiobooks an author and a narrator, ROMs a platform — and every
// layout in DESIGN §5 is built from exactly those fields.
//
// Until this file existed, `toParsed` copied nine fields and left eight at
// their zero values, so `buildROM` refused every ROM outright and comics,
// ebooks, music and audiobooks all filed to wrong paths without an error.
// Four reviewers found it independently, which is the signal that a gap is
// structural rather than obscure.

var (
	reScenePairYear = regexp.MustCompile(`^(.+?)\s*-\s*(.+?)\s*[\(\[]?((?:19|20)\d{2})[\)\]]?\s*$`)
	reDiscNo        = regexp.MustCompile(`(?i)\b(\d+)\s*CD\b|\bCD\s*(\d+)\b|\bDisc\s*(\d+)\b`)

	reVolume  = regexp.MustCompile(`(?i)\bv(?:ol)?\.?\s?(\d{1,3})(?:[^\d.]|$)`)
	reIssueNo = regexp.MustCompile(`(?i)(?:^|\s)#?(\d{2,4})(?:\s|$|\()`)
	reChapter = regexp.MustCompile(`(?i)\bchapter\s*(\d{1,5})\b`)

	reNarratorB = regexp.MustCompile(`\{([^}]+)\}`)
	reNarratorP = regexp.MustCompile(`\(([A-Z][a-z]+(?:\.?\s+[A-Z]\.?)*\s+[A-Z][a-z]+)\)`)
	reLastFirst = regexp.MustCompile(`^([A-Z][\w'-]+),\s*([A-Z][\w'\-. ]+)$`)
	reSeriesIdx = regexp.MustCompile(`^(.+?)\s+(\d{1,3})$`)

	reRegionTag = regexp.MustCompile(`\((USA|Europe|Japan|World|Australia|Korea|Brazil|Spain|France|Germany|Italy|China|Taiwan)[^)]*\)`)
	reTrailTags = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]\s*$`)
)

// enrich fills the type-specific fields of p from the payload.
//
// It runs after the type is decided, because the same token means different
// things per type: "v01" is a comic volume and a version string in a film
// name, and "Artist - Album" is a music convention and an author-title one.
func enrich(p *media.Parsed, t media.Type, fs media.FileSet, info release.Info) {
	name := stripKnownExt(lastPathComponent(displayName(fs)))

	switch t {
	case media.Music:
		enrichMusic(p, name, fs)
	case media.Audiobook:
		enrichAudiobook(p, name, fs)
	case media.Ebook:
		enrichEbook(p, name, fs)
	case media.Comic:
		enrichComic(p, name)
	case media.ROM:
		enrichROM(p, name, fs)
	}
}

// enrichMusic recovers artist, album and year from the folder conventions.
//
// Navidrome reads tags, not paths, so a wrong guess here is cosmetic rather
// than invisible — but "Artist/Album (Year)" is what the user browses on
// disk, and collapsing it to "Title/Title" makes the library unusable by
// hand.
func enrichMusic(p *media.Parsed, name string, fs media.FileSet) {
	work := stripSceneTrailer(name)

	if m := reScenePairYear.FindStringSubmatch(work); m != nil {
		p.Artist = tidy(m[1])
		p.Album = tidy(reTrailTags.ReplaceAllString(m[2], ""))
		if y, err := strconv.Atoi(m[3]); err == nil {
			p.Year = y
		}
	} else if i := strings.Index(work, " - "); i > 0 {
		p.Artist = tidy(work[:i])
		p.Album = stripTrailingGroups(work[i+3:], p)
	} else {
		p.Album = stripTrailingGroups(work, p)
	}

	if reVariousArt.MatchString(name) {
		// Navidrome groups on ALBUMARTIST, and a compilation whose tracks
		// each carry a different artist only coheres under this one.
		p.Artist = "Various Artists"
	}
	if p.Artist == "" {
		p.Artist = "Unknown Artist"
	}
	if p.Album == "" {
		p.Album = p.Title
	}
	if p.Album == "" {
		p.Album = "Unknown Album"
	}
}

// enrichAudiobook recovers author, series, sequence and narrator.
//
// Audiobookshelf's folder grammar IS its metadata parser, so each field
// here lands in a specific ABS slot and a shifted field is a mislabelled
// book rather than a missing one.
func enrichAudiobook(p *media.Parsed, name string, fs media.FileSet) {
	work := name

	if m := reNarratorB.FindStringSubmatch(work); m != nil && !reMetadataBrace.MatchString(m[1]) {
		p.Narrator = tidy(m[1])
		work = strings.Replace(work, m[0], "", 1)
	} else if m := reNarratorP.FindStringSubmatch(work); m != nil {
		p.Narrator = tidy(m[1])
		work = strings.Replace(work, m[0], "", 1)
	}
	work = reAudiobookWord.ReplaceAllString(work, "")

	// The torrent's own directory tree often carries {Author}/{Series}/{Book}
	// already. Prefer it: it is what the uploader asserted, and it is more
	// reliable than splitting one flat string.
	if segs := pathSegments(fs); len(segs) >= 2 {
		p.Author = tidy(segs[0])
		if len(segs) >= 3 {
			p.Series = tidy(segs[1])
		}
	}

	if i := strings.Index(work, " - "); i > 0 {
		left, right := tidy(work[:i]), tidy(work[i+3:])
		if p.Author == "" {
			p.Author = normalizePerson(left)
		}
		if t := tidy(reTrailTags.ReplaceAllString(right, "")); t != "" {
			p.Title = t
		}
	} else if p.Title == "" {
		p.Title = tidy(reTrailTags.ReplaceAllString(work, ""))
	}

	if m := reSeriesIdx.FindStringSubmatch(p.Series); m != nil {
		p.Series = tidy(m[1])
		p.Volume, _ = strconv.Atoi(m[2])
	}
	if p.Title == "" {
		p.Title = tidy(work)
	}
}

func enrichEbook(p *media.Parsed, name string, fs media.FileSet) {
	work := tidy(reTrailTags.ReplaceAllString(name, ""))

	if segs := pathSegments(fs); len(segs) >= 2 {
		p.Author = tidy(segs[0])
	}

	if i := strings.Index(work, " - "); i > 0 {
		left, right := tidy(work[:i]), tidy(work[i+3:])
		// "Sanderson, Brandon - Mistborn 01 - The Final Empire": a
		// "Last, First" author is unambiguous, so it decides the order.
		// Otherwise assume "Author - Title", which is the commoner form —
		// and record no author rather than guess when even that is shaky.
		if reLastFirst.MatchString(left) {
			p.Author = normalizePerson(left)
			p.Title = right
		} else if reLastFirst.MatchString(right) {
			p.Author = normalizePerson(right)
			p.Title = left
		} else {
			if p.Author == "" {
				p.Author = left
			}
			p.Title = right
		}
	} else if p.Title == "" || p.Title == name {
		p.Title = work
	}

	// "Mistborn 01 - The Final Empire" leaves a series and an index in the
	// middle segment.
	if i := strings.Index(p.Title, " - "); i > 0 {
		if m := reSeriesIdx.FindStringSubmatch(tidy(p.Title[:i])); m != nil {
			p.Series = tidy(m[1])
			p.Volume, _ = strconv.Atoi(m[2])
			p.Title = tidy(p.Title[i+3:])
		}
	}
	if p.Title == "" {
		p.Title = work
	}
}

// enrichComic recovers series, volume and issue.
//
// The series must NOT keep the volume token: Komga creates one Series per
// directory that directly contains books, so a folder named "Saga v01"
// makes a 34-volume run into 34 separate series.
func enrichComic(p *media.Parsed, name string) {
	work := name

	if m := reChapter.FindStringSubmatch(work); m != nil {
		p.Issue = m[1]
	}
	if m := reVolume.FindStringSubmatch(work); m != nil {
		p.Volume, _ = strconv.Atoi(m[1])
		work = work[:strings.Index(work, m[0])]
	} else if m := reIssueNo.FindStringSubmatch(work); m != nil && p.Issue == "" {
		p.Issue = strings.TrimLeft(m[1], "0")
		if p.Issue == "" {
			p.Issue = "0"
		}
		if i := strings.LastIndex(work, m[1]); i > 0 {
			work = work[:i]
		}
	}

	series := tidy(reTrailTags.ReplaceAllString(work, ""))
	series = tidy(reChapter.ReplaceAllString(series, ""))
	series = strings.TrimRight(series, " -_.")
	if series != "" {
		p.Series = series
	} else {
		p.Series = p.Title
	}
	if p.Title == "" {
		p.Title = p.Series
	}
}

// enrichROM recovers the platform, which RomM's layout REQUIRES.
//
// Three sources, most reliable first. A parent directory naming the
// platform is the strongest available signal because the uploader asserted
// it; an unambiguous extension is next; and a genuinely ambiguous one —
// .iso spans six consoles and Linux, .bin spans PS1 tracks and BIOS images
// — yields nothing, which routes the item to review rather than filing
// someone's distribution image into a games library.
func enrichROM(p *media.Parsed, name string, fs media.FileSet) {
	p.Title = tidy(reRegionTag.ReplaceAllString(name, ""))
	p.Title = tidy(reTrailTags.ReplaceAllString(p.Title, ""))
	if p.Title == "" {
		p.Title = name
	}

	for _, seg := range pathSegments(fs) {
		if slug, ok := platformBySlug[strings.ToLower(seg)]; ok {
			p.Platform = slug
			return
		}
	}
	for _, f := range fs.Files {
		if slug, ok := romPlatform[f.Ext]; ok {
			p.Platform = slug
			return
		}
	}
	// Deliberately leaves Platform empty for ambiguous extensions. layout
	// then refuses, and the item is surfaced for review.
}

// pathSegments returns the directory components common to the payload,
// outermost first, excluding the filename.
func pathSegments(fs media.FileSet) []string {
	if len(fs.Files) == 0 {
		return nil
	}
	dir := path.Dir(fs.Files[0].RelPath)
	if dir == "." || dir == "/" {
		// A single-level payload: the orphan's own name is the container.
		if fs.Name != "" {
			return strings.Split(strings.Trim(fs.Name, "/"), "/")
		}
		return nil
	}
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	if fs.Name != "" && !strings.Contains(fs.Name, "/") {
		segs = append([]string{fs.Name}, segs...)
	}
	return segs
}

func lastPathComponent(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// stripSceneTrailer removes the trailing "-GROUP" and the release-metadata
// run that scene music names carry after the year.
func stripSceneTrailer(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	fields := strings.Split(s, "-")
	if len(fields) < 3 {
		return tidy(s)
	}
	// Keep everything up to the first field that is pure release metadata.
	keep := []string{}
	for _, f := range fields {
		t := strings.ToLower(tidy(f))
		if isReleaseToken(t) {
			break
		}
		keep = append(keep, tidy(f))
	}
	if len(keep) < 2 {
		return tidy(s)
	}
	return strings.Join(keep, " - ")
}

func isReleaseToken(t string) bool {
	switch t {
	case "web", "flac", "mp3", "cd", "vinyl", "remastered", "24bit", "cbr", "vbr",
		"promo", "single", "ep", "advance", "bootleg", "live", "reissue", "limited":
		return true
	}
	if len(t) == 4 {
		if y, err := strconv.Atoi(t); err == nil && y >= 1900 && y <= 2100 {
			return true
		}
	}
	return strings.HasSuffix(t, "khz") || strings.HasSuffix(t, "kbps") ||
		strings.HasSuffix(t, "cd") && len(t) <= 4
}

// normalizePerson turns "Sanderson, Brandon" into "Brandon Sanderson".
// Audiobookshelf and Komga both display the folder name verbatim.
func normalizePerson(s string) string {
	if m := reLastFirst.FindStringSubmatch(tidy(s)); m != nil {
		return tidy(m[2]) + " " + tidy(m[1])
	}
	return tidy(s)
}

// stripTrailingGroups removes EVERY trailing bracket group, not just the
// last one, and lifts a year out of any of them.
//
// "OK Computer (1997) [FLAC]" has two, and stripping once leaves
// "OK Computer (1997)" as the album name — so the folder disagrees with
// the tags Navidrome actually groups on, at exactly the moment the user is
// trying to work out why their album is split in two.
func stripTrailingGroups(s string, p *media.Parsed) string {
	for {
		next := reTrailTags.ReplaceAllString(s, "")
		if next == s {
			break
		}
		// Recover a year from the group being discarded.
		if m := reYearGroup.FindStringSubmatch(s[len(next):]); m != nil && p.Year == 0 {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 1888 && y <= 2100 {
				p.Year = y
			}
		}
		s = next
	}
	return tidy(s)
}

var reYearGroup = regexp.MustCompile(`((?:19|20)\d{2})`)

func tidy(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " -_.")
}

// platformBySlug lets a parent directory name a RomM platform directly.
var platformBySlug = map[string]string{}

func init() {
	for _, slug := range romPlatform {
		platformBySlug[slug] = slug
	}
	// Common directory spellings that are not the slug itself.
	for dir, slug := range map[string]string{
		"snes": "snes", "supernintendo": "snes", "sfc": "snes",
		"nes": "nes", "famicom": "nes",
		"n64": "n64", "nintendo64": "n64",
		"gb": "gb", "gameboy": "gb",
		"gbc": "gbc", "gba": "gba",
		"genesis": "genesis", "megadrive": "genesis", "md": "genesis",
		"psx": "psx", "ps1": "psx", "playstation": "psx",
		"gamecube": "ngc", "gc": "ngc",
		"tg16": "tg16", "pcengine": "tg16", "turbografx16": "tg16",
		"lynx": "lynx", "sega32": "sega32",
	} {
		platformBySlug[dir] = slug
	}
}
