// Package release parses scene and non-scene release names.
//
// It is a pure function over a string. It performs no I/O, consults no
// metadata provider, and never guesses: a name it cannot read yields a low
// Confidence, and I9 routes low confidence to review rather than to a
// destination (DESIGN §4.7).
//
// The hard part is not the common case. It is that "2012.2009.1080p" has a
// title that is a year, "Blade.Runner.2049.S01E01" is television despite
// looking like a film, and "The.Godfather.Part.II" must not be split by a
// multi-part matcher. Each of those is a corpus case, and each one broke an
// earlier version of this file.
package release

import (
	"regexp"
	"strconv"
	"strings"
)

// Info is what a name yielded. Zero values mean "not found", never "zero".
type Info struct {
	Title string
	Year  int

	// Television.
	IsTV       bool
	Season     int
	HasSeason  bool
	Episodes   []int
	AirDate    string // YYYY-MM-DD
	Absolute   int    // anime absolute numbering
	SeasonPack bool
	SeriesPack bool

	Edition  string
	Language string
	Group    string
	IMDB     string

	// Confidence in the *parse*, which is not the same as confidence in the
	// classification. Knowing a payload is TV is not knowing which episode.
	Confidence float64
}

// Season/episode notations, most specific first. Order matters: S01E01E02
// must be tried before S01E01, or the second episode is silently dropped.
var (
	// The separator class here excludes the hyphen deliberately. A hyphen
	// means a *span* (S01E01-E03 is three episodes) and a list means
	// exactly the episodes named (S01E01E02 is two). Allowing '-' here
	// makes the list matcher claim the span and yield [1, 3] — the middle
	// episode silently vanishes, and nothing downstream can notice.
	reMultiEpList  = regexp.MustCompile(`(?i)\bS(\d{1,4})((?:[\s._]?E\d{1,3}){2,})\b`)
	reEpisodeRange = regexp.MustCompile(`(?i)\bS(\d{1,4})[\s._-]?E(\d{1,3})[\s._-]*-[\s._-]*E?(\d{1,3})\b`)
	reStdEpisode   = regexp.MustCompile(`(?i)\bS(\d{1,4})[\s._-]?E(\d{1,3})\b`)
	reAltEpisode   = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	reWordEpisode  = regexp.MustCompile(`(?i)\bSeason[\s._-]*(\d{1,4})[\s._-]*Episode[\s._-]*(\d{1,3})\b`)
	reSeasonOnly   = regexp.MustCompile(`(?i)\bS(\d{1,4})\b`)
	reSeasonWord   = regexp.MustCompile(`(?i)\bSeason[\s._-]*(\d{1,4})\b`)
	// The space in the separator class matters: scene dots are normalised to
	// spaces before this runs, so "2024.03.14" reaches us as "2024 03 14".
	reAirDate     = regexp.MustCompile(`\b((?:19|20)\d{2})[-._ ](\d{2})[-._ ](\d{2})\b`)
	reCompleteSer = regexp.MustCompile(`(?i)\b(COMPLETE[\s._-]*SERIES|THE[\s._-]*COMPLETE[\s._-]*SERIES)\b`)
	reEpNums      = regexp.MustCompile(`(?i)E(\d{1,3})`)

	// Anime: "[Group] Series - 12 (1080p) [CRC]". The leading bracket group
	// is the fansub group, the trailing one a CRC32 — neither is the title.
	reAnime = regexp.MustCompile(`^\[([^\]]+)\]\s*(.+?)\s*-\s*(\d{1,4})(?:v\d)?\s*(?:\(|\[|$)`)

	reYearParen = regexp.MustCompile(`[\(\[]((?:19|20)\d{2})[\)\]]`)
	reYearBare  = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	reIMDB      = regexp.MustCompile(`(?i)\{imdb-(tt\d+)\}`)
	reGroup     = regexp.MustCompile(`-([A-Za-z0-9]+)$`)
)

// qualityTokens are release-metadata words that are never part of a title.
// Membership is exact, lowercased, after separator normalisation.
var qualityTokens = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`
		480p 576p 720p 1080p 1440p 2160p 4320p 4k 8k uhd hd sd
		bluray blu-ray brrip bdrip bdremux remux web web-dl webdl webrip hdtv pdtv dvdrip dvd
		hddvd vhsrip tvrip hdrip camrip cam ts tc r5 screener scr
		x264 x265 h264 h265 h.264 h.265 hevc avc xvid divx vp9 av1 10bit 8bit
		dts dts-hd dts-x truehd atmos ac3 eac3 ddp dd aac mp3 flac opus lpcm pcm
		ma hr es 5.1 7.1 2.0 1.0 6.1
		hdr hdr10 hdr10+ dv dolbyvision sdr hlg
		proper repack rerip internal limited nfofix dirfix subfix real
		multi dual subbed dubbed subs sub nosub
		complete boxset
		digital retail scan webscan
		amzn nf dsnp hmax atvp hulu pcok stan cr
		ita eng ger fre spa jpn kor chi rus pol dan swe nor fin dut cze hun tur ara heb hin
	`) {
		qualityTokens[t] = true
	}
}

// editionTokens map a normalised token run to the edition label the layout
// engine emits. Plex's {edition-...} tag is off by default (BRIEF Q22) but
// the parse must still recover it, because it is title-adjacent and would
// otherwise contaminate the title.
var editionPatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?i)\bextended(\s+(cut|edition|version))?\b`), "Extended"},
	{regexp.MustCompile(`(?i)\bdirectors?[\s._']*cut\b`), "Director's Cut"},
	{regexp.MustCompile(`(?i)\bfinal[\s._]*cut\b`), "Final Cut"},
	{regexp.MustCompile(`(?i)\btheatrical([\s._]*cut)?\b`), "Theatrical"},
	{regexp.MustCompile(`(?i)\bimax\b`), "IMAX"},
	{regexp.MustCompile(`(?i)\bunrated\b`), "Unrated"},
	{regexp.MustCompile(`(?i)\bultimate([\s._]*edition)?\b`), "Ultimate"},
	{regexp.MustCompile(`(?i)\bspecial[\s._]*edition\b`), "Special Edition"},
	{regexp.MustCompile(`(?i)\bdespecialized\b`), "Despecialized"},
	{regexp.MustCompile(`(?i)\bremastered\b`), "Remastered"},
	{regexp.MustCompile(`(?i)\bcriterion\b`), "Criterion"},
}

var languageNames = map[string]string{
	"french": "French", "german": "German", "italian": "Italian",
	"spanish": "Spanish", "japanese": "Japanese", "korean": "Korean",
	"russian": "Russian", "dutch": "Dutch", "swedish": "Swedish",
	"nordic": "Nordic", "danish": "Danish", "norwegian": "Norwegian",
	"finnish": "Finnish", "polish": "Polish", "czech": "Czech",
	"hindi": "Hindi", "chinese": "Chinese", "portuguese": "Portuguese",
}

// Parse reads a release name. base should already have any container
// extension removed by the caller when the payload is a single file.
func Parse(name string) Info {
	in := Info{}
	if strings.TrimSpace(name) == "" {
		return in
	}

	// Work on the last path component: a corpus case supplies
	// "Chernobyl.S01.../Chernobyl.S01E05...mkv" and the file is what
	// carries the episode. The parent is consulted separately by the
	// classifier, which has the whole FileSet.
	work := name
	if i := strings.LastIndexByte(work, '/'); i >= 0 {
		work = work[i+1:]
	}

	if m := reIMDB.FindStringSubmatch(work); m != nil {
		in.IMDB = m[1]
		work = strings.Replace(work, m[0], " ", 1)
	}

	// Anime is checked before separator normalisation, because the bracket
	// structure is the signal and normalising destroys it.
	if m := reAnime.FindStringSubmatch(work); m != nil {
		n, _ := strconv.Atoi(m[3])
		// A 4-digit "episode" is a year; a bracketed group plus a year is a
		// normal release, not anime.
		if n > 0 && n < 2000 {
			in.IsTV = true
			in.Absolute = n
			in.Group = m[1]
			in.Title = cleanTitle(normalizeSeparators(m[2]))
			// Absolute numbering cannot be mapped to season/episode without
			// a lookup v1 does not have, so this is deliberately not a
			// confident parse — it routes to review (DESIGN §4.7).
			in.Confidence = 0.55
			return in
		}
	}

	norm := normalizeSeparators(work)

	// The earliest structural marker ends the title. Everything the parser
	// does afterwards is about which marker that is.
	titleEnd := len(norm)
	note := func(idx int) {
		if idx >= 0 && idx < titleEnd {
			titleEnd = idx
		}
	}

	parseTV(&in, norm, note)

	if !in.IsTV {
		parseMovieYear(&in, norm, note)
	} else {
		parseSeriesYear(&in, norm, titleEnd, note)
	}

	for _, ep := range editionPatterns {
		if loc := ep.re.FindStringIndex(norm); loc != nil {
			// An edition token inside the title region is title text
			// ("Star Wars ... Despecialized" is, "Blade Runner 1982 Final
			// Cut" is not). Only accept it after the title has ended.
			if loc[0] >= titleEnd {
				in.Edition = ep.label
				break
			}
		}
	}

	lower := strings.ToLower(norm)
	for tok, label := range languageNames {
		if idx := strings.Index(lower, tok); idx >= 0 && idx >= titleEnd {
			in.Language = label
			break
		}
	}

	if m := reGroup.FindStringSubmatch(strings.TrimSpace(work)); m != nil && in.Group == "" {
		if !qualityTokens[strings.ToLower(m[1])] {
			in.Group = m[1]
		}
	}

	if in.Title == "" {
		in.Title = cleanTitle(norm[:titleEnd])
	}

	scoreConfidence(&in, titleEnd, len(norm))
	return in
}

// parseTV sets every television field and reports where the title ended.
func parseTV(in *Info, norm string, note func(int)) {
	// Multi-episode list: S01E01E02. Checked first — reStdEpisode would
	// match the same text and lose E02.
	if m := reMultiEpList.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		for _, e := range reEpNums.FindAllStringSubmatch(norm[m[2]:m[5]], -1) {
			n, _ := strconv.Atoi(e[1])
			in.Episodes = append(in.Episodes, n)
		}
		note(m[0])
		return
	}

	// Episode span: S01E01-E03 expands to 1,2,3. Reading it as subtraction
	// yields episode -2, which is the kind of bug that files nothing and
	// explains nothing.
	if m := reEpisodeRange.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		lo, _ := strconv.Atoi(norm[m[4]:m[5]])
		hi, _ := strconv.Atoi(norm[m[6]:m[7]])
		if hi >= lo && hi-lo < 100 {
			for e := lo; e <= hi; e++ {
				in.Episodes = append(in.Episodes, e)
			}
		} else {
			in.Episodes = []int{lo}
		}
		note(m[0])
		return
	}

	if m := reWordEpisode.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		e, _ := strconv.Atoi(norm[m[4]:m[5]])
		in.Episodes = []int{e}
		note(m[0])
		return
	}

	if m := reStdEpisode.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		e, _ := strconv.Atoi(norm[m[4]:m[5]])
		in.Episodes = []int{e}
		// Episode 00 marks a special. Plex and Jellyfin both file specials
		// under Season 00 regardless of which season they aired alongside,
		// so "S13E00" belongs in Season 00, not Season 13. Filing it under
		// 13 puts it in a season the series may not even have.
		if e == 0 {
			in.Season = 0
		}
		note(m[0])
		return
	}

	if m := reAltEpisode.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		e, _ := strconv.Atoi(norm[m[4]:m[5]])
		in.Episodes = []int{e}
		note(m[0])
		return
	}

	// Date-based shows. These route to review: Plex's own example files
	// dailies under a real season number the date does not yield
	// (DESIGN §5.2).
	if m := reAirDate.FindStringSubmatchIndex(norm); m != nil {
		y, _ := strconv.Atoi(norm[m[2]:m[3]])
		mo, _ := strconv.Atoi(norm[m[4]:m[5]])
		d, _ := strconv.Atoi(norm[m[6]:m[7]])
		if plausibleYear(y) && mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			in.IsTV = true
			in.AirDate = norm[m[2]:m[3]] + "-" + norm[m[4]:m[5]] + "-" + norm[m[6]:m[7]]
			note(m[0])
			return
		}
	}

	if loc := reCompleteSer.FindStringIndex(norm); loc != nil {
		in.IsTV = true
		in.SeriesPack = true
		note(loc[0])
		return
	}

	if m := reSeasonWord.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		in.SeasonPack = true
		note(m[0])
		return
	}

	// Bare S01. Last, and deliberately so: "S01" is a short token and
	// matching it early would claim names that a more specific rule owns.
	if m := reSeasonOnly.FindStringSubmatchIndex(norm); m != nil {
		in.IsTV = true
		in.Season, _ = strconv.Atoi(norm[m[2]:m[3]])
		in.HasSeason = true
		in.SeasonPack = true
		note(m[0])
	}
}

// parseMovieYear finds the release year, which is also where a film's title
// ends.
func parseMovieYear(in *Info, norm string, note func(int)) {
	// A parenthesised year always wins. "Blade Runner 2049 (2017)" has two
	// year-shaped tokens and only one of them is the year.
	if m := reYearParen.FindStringSubmatchIndex(norm); m != nil {
		y, _ := strconv.Atoi(norm[m[2]:m[3]])
		if plausibleYear(y) {
			in.Year = y
			note(m[0])
			return
		}
	}

	all := reYearBare.FindAllStringSubmatchIndex(norm, -1)
	if len(all) == 0 {
		return
	}

	// Two adjacent year-shaped tokens means the first is the title:
	// "2012.2009.1080p" is the film 2012, released 2009. Taking the first
	// match would file it as a 2012 film called nothing.
	if len(all) >= 2 && all[0][0] == 0 {
		gap := strings.TrimSpace(norm[all[0][1]:all[1][0]])
		if gap == "" {
			y, _ := strconv.Atoi(norm[all[1][2]:all[1][3]])
			if plausibleYear(y) {
				in.Year = y
				note(all[1][0])
				return
			}
		}
	}

	// Otherwise the last plausible year that still leaves a title standing.
	for i := len(all) - 1; i >= 0; i-- {
		y, _ := strconv.Atoi(norm[all[i][2]:all[i][3]])
		if !plausibleYear(y) {
			continue
		}
		if strings.TrimSpace(cleanTitle(norm[:all[i][0]])) == "" {
			continue
		}
		in.Year = y
		note(all[i][0])
		return
	}
}

func plausibleYear(y int) bool { return y >= 1888 && y <= 2100 }

// maxSeriesYear bounds what a year token immediately before an episode code
// may mean.
//
// "Show Name 2023 S01E01" carries a series-disambiguation year: two shows
// share a title and the year separates them. "Blade Runner 2049 S01E01" does
// not — 2049 is part of the title. Locally there is no other signal that
// tells these apart, and a metadata lookup is out of scope (§1.2), so the
// discriminator is whether the year could plausibly be one a series had
// already started in.
//
// This is a constant rather than time.Now() because classify and everything
// it depends on are pure: no I/O, no clock (DESIGN §2.3). A clock here would
// make the 118-case corpus produce different results on different days,
// which is exactly the property that makes the corpus worth having.
//
// It will need raising eventually. When it does, a corpus case will fail,
// which is the intended way to find out.
const maxSeriesYear = 2035

// parseSeriesYear strips a series-disambiguation year from the title region.
func parseSeriesYear(in *Info, norm string, titleEnd int, note func(int)) {
	loc := reYearBare.FindStringIndex(norm)
	if loc == nil || loc[0] >= titleEnd {
		return
	}
	y, _ := strconv.Atoi(norm[loc[0]:loc[1]])
	if !plausibleYear(y) || y > maxSeriesYear {
		// A future year is title text. Leave it where it is.
		return
	}
	// Only when the year sits immediately before the episode code: a year
	// in the middle of a title ("Show 2023 Extra S01E01") is not this case.
	if strings.TrimSpace(norm[loc[1]:titleEnd]) != "" {
		return
	}
	in.Year = y
	note(loc[0])
}

// normalizeSeparators turns scene dots and underscores into spaces without
// destroying tokens that legitimately contain a dot.
func normalizeSeparators(s string) string {
	// Protect decimal-bearing technical tokens before the dot massacre.
	protect := []struct{ from, to string }{
		{"H.264", "\x01H264\x01"}, {"h.264", "\x01h264\x01"},
		{"H.265", "\x01H265\x01"}, {"h.265", "\x01h265\x01"},
		{"5.1", "\x015_1\x01"}, {"7.1", "\x017_1\x01"},
		{"2.0", "\x012_0\x01"}, {"6.1", "\x016_1\x01"},
		{"DD+", "\x01DDP\x01"},
	}
	for _, p := range protect {
		s = strings.ReplaceAll(s, p.from, p.to)
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '.', '_':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	for _, p := range protect {
		out = strings.ReplaceAll(out, p.to, strings.ReplaceAll(p.from, ".", "."))
	}
	out = strings.ReplaceAll(out, "\x01", "")
	return collapseSpaces(out)
}

func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// cleanTitle strips release metadata that survived the title boundary, then
// trims punctuation the boundary left dangling.
func cleanTitle(s string) string {
	s = collapseSpaces(s)
	if s == "" {
		return ""
	}

	// Drop trailing bracket groups: "[YTS.MX]", "(Digital)", "{Narrator}".
	s = regexp.MustCompile(`[\(\[\{][^\)\]\}]*[\)\]\}]\s*$`).ReplaceAllString(s, "")
	s = collapseSpaces(s)

	fields := strings.Fields(s)
	// Trim quality tokens from the right only. A left-to-right filter would
	// delete "4K" from a title that is genuinely called that.
	end := len(fields)
	for end > 0 && qualityTokens[strings.ToLower(strings.Trim(fields[end-1], "()[]{}-"))] {
		end--
	}
	fields = fields[:end]

	out := strings.Join(fields, " ")
	out = strings.Trim(out, " -_.")
	// A trailing "-GRP" left by a group suffix on an unseparated name.
	out = regexp.MustCompile(`\s+-\s*[A-Za-z0-9]+$`).ReplaceAllString(out, "")
	return collapseSpaces(out)
}

// scoreConfidence rates the parse, not the classification.
func scoreConfidence(in *Info, titleEnd, total int) {
	if in.Title == "" {
		in.Confidence = 0
		return
	}
	c := 0.30

	switch {
	case in.IsTV && in.HasSeason && len(in.Episodes) > 0:
		c = 0.95
	case in.IsTV && in.HasSeason:
		c = 0.85 // season pack
	case in.IsTV && in.AirDate != "":
		// Deliberately below the auto threshold: the date does not yield a
		// season, so this must be reviewed rather than filed.
		c = 0.60
	case in.IsTV && in.Absolute > 0:
		c = 0.55
	case in.IsTV && in.SeriesPack:
		c = 0.70
	case in.Year > 0:
		c = 0.90
	}

	// A title that consumed the entire string means no release metadata was
	// found at all, which usually means this is not a release name.
	if titleEnd >= total && in.Year == 0 && !in.IsTV {
		c = 0.20
	}
	if in.IMDB != "" {
		c += 0.05
	}
	if c > 1 {
		c = 1
	}
	in.Confidence = c
}
