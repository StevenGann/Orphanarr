// Package layout turns a classification into destination paths.
//
// It is a pure function: (MediaType, Parsed, []SourceFile, Library) ->
// []PlannedFile (DESIGN §2.3). It performs no I/O and does not know how the
// files will be placed — a copy and a hardlink produce byte-identical trees,
// which is why the 2026-08-07 placement inversion changed nothing here.
package layout

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/StevenGann/Orphanarr/internal/classify/release"
	"github.com/StevenGann/Orphanarr/internal/media"
)

// Library is one configured destination.
type Library struct {
	Type media.Type
	Root string
	Opts Options

	// OneshotsDir is Komga's one-shots directory name. Empty means the user
	// has not confirmed one, which is not the same as "use the default":
	// Komga's own default is null, and emitting a folder it does not
	// recognise produces one series containing every standalone work.
	OneshotsDir string

	// AcceptedExts is the per-library ingest rule (DESIGN §4.2). A file the
	// target server cannot ingest is REFUSED, not warned about — a warning
	// is not a gate, so a warned item auto-files into invisibility. Empty
	// means accept everything.
	AcceptedExts map[string]bool

	// EmitEditionTags controls Plex's {edition-...} suffix. Off by default:
	// it needs Plex Pass and is visible noise in Jellyfin (BRIEF Q22).
	EmitEditionTags bool
}

// PlannedFile is one intended placement. Nothing here has happened yet.
type PlannedFile struct {
	Src      media.SourceFile
	DstRel   string
	Dst      string
	Warnings []Warning
	Skip     bool
	// SkipReason is a stable event code, not prose, so the dashboard can
	// group by it and the user can act on it.
	SkipReason string
}

// Result is a whole layout decision.
type Result struct {
	Files    []PlannedFile
	Warnings []Warning
	// NeedsReview forces the plan into the review queue regardless of
	// score, for cases the layout itself cannot resolve.
	NeedsReview  bool
	ReviewReason string
}

// Build lays out an orphan. It never returns a path outside lib.Root; the
// executor re-asserts that too (I6), because a single check is a single
// point of failure for the property that matters most.
func Build(t media.Type, p media.Parsed, files []media.SourceFile, lib Library) (Result, error) {
	var res Result

	payload := make([]media.SourceFile, 0, len(files))
	for _, f := range files {
		if IsSampleOrExtra(f.RelPath) {
			res.Files = append(res.Files, PlannedFile{
				Src: f, Skip: true, SkipReason: "SKIP_SAMPLE",
			})
			continue
		}
		if len(lib.AcceptedExts) > 0 && !lib.AcceptedExts[f.Ext] {
			// Refuse, do not warn. I9 gates on confidence, cardinality,
			// mixed and rootlessness — not on warnings.
			res.Files = append(res.Files, PlannedFile{
				Src: f, Skip: true, SkipReason: "SKIP_UNSUPPORTED_FORMAT",
			})
			continue
		}
		payload = append(payload, f)
	}
	if len(payload) == 0 {
		res.NeedsReview = true
		res.ReviewReason = "no ingestible files after applying the library's ingest rule"
		return res, nil
	}

	var (
		placed []PlannedFile
		err    error
	)
	switch t {
	case media.Movie:
		placed, err = buildMovie(p, payload, lib)
	case media.TV:
		placed, err = buildTV(p, payload, lib, &res)
	case media.Music:
		placed, err = buildVerbatim(p, payload, lib, musicFolder(p))
	case media.Audiobook:
		placed, err = buildVerbatim(p, payload, lib, audiobookFolder(p))
	case media.Comic:
		placed, err = buildComic(p, payload, lib)
	case media.Ebook:
		placed, err = buildEbook(p, payload, lib)
	case media.ROM:
		placed, err = buildROM(p, payload, lib)
	default:
		return res, fmt.Errorf("layout: no layout for type %q", t)
	}
	if err != nil {
		return res, err
	}
	res.Files = append(res.Files, placed...)

	for i := range res.Files {
		res.Warnings = append(res.Warnings, res.Files[i].Warnings...)
	}
	return res, nil
}

// join builds an absolute destination and asserts containment. Every layout
// funnels through it so that no branch can forget.
func join(lib Library, parts ...string) (rel, abs string, warns []Warning, err error) {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		s, w, err := SanitizeComponent(p, lib.Opts)
		if err != nil {
			return "", "", nil, err
		}
		warns = append(warns, w...)
		clean = append(clean, s)
	}
	if len(clean) == 0 {
		return "", "", warns, ErrEmptyName
	}
	rel = path.Join(clean...)
	abs = path.Join(lib.Root, rel)

	// Belt and braces: even after per-component sanitisation, assert the
	// joined result did not escape. Cheap, and the failure it prevents is
	// unrecoverable.
	if abs != lib.Root && !strings.HasPrefix(abs, strings.TrimSuffix(lib.Root, "/")+"/") {
		return "", "", warns, fmt.Errorf("%w: %s", ErrTraversal, abs)
	}
	return rel, abs, warns, nil
}

func titleYear(p media.Parsed) string {
	if p.Year > 0 {
		return fmt.Sprintf("%s (%d)", p.Title, p.Year)
	}
	return p.Title
}

// buildMovie emits the Plex and Jellyfin shared form:
//
//	{root}/{Title} ({Year})/{Title} ({Year}).ext
func buildMovie(p media.Parsed, files []media.SourceFile, lib Library) ([]PlannedFile, error) {
	folder := titleYear(p)
	stem := folder
	if lib.EmitEditionTags && p.Edition != "" {
		stem += fmt.Sprintf(" {edition-%s}", p.Edition)
	}

	main := largest(files)
	var out []PlannedFile
	for _, f := range files {
		name := stem + f.Ext
		if f.RelPath != main.RelPath {
			// Subtitles and sidecars keep their own distinguishing suffix
			// so a second .srt does not collide with the first.
			name = stem + suffixFor(f, main) + f.Ext
		}
		name, tw := TruncateComponent(strings.TrimSuffix(name, f.Ext), f.Ext, "", lib.Opts)
		rel, abs, w, err := join(lib, folder, name)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: append(w, tw...)})
	}
	return out, nil
}

// buildTV emits {root}/{Series} ({Year})/Season {NN}/{Series} - S00E00.ext.
func buildTV(p media.Parsed, files []media.SourceFile, lib Library, res *Result) ([]PlannedFile, error) {
	// Neither a date-based episode nor anime absolute numbering yields a
	// season number, and Plex's own example files dailies under a real
	// season the date does not supply. Guessing puts the episode in a
	// season the series may not have, so these go to review (DESIGN §5.2).
	if p.AirDate != "" {
		res.NeedsReview = true
		res.ReviewReason = "date-based episode: no season number is derivable from " + p.AirDate
	}
	if p.Absolute > 0 {
		res.NeedsReview = true
		res.ReviewReason = "anime absolute numbering: mapping to season/episode needs a lookup v1 does not have"
	}

	series := titleYear(p)

	var out []PlannedFile
	for _, f := range files {
		// EACH FILE is parsed for its own season and episode.
		//
		// A season pack carries one name for the torrent and a different
		// episode in every file. Applying the torrent-level parse to all of
		// them emitted the same destination for all 10 episodes — and with
		// the default skip collision policy, 9 were silently dropped and
		// the survivor was numbered E00, which matches nothing in any
		// agent's table. DESIGN §5.2: "Season packs produce one step per
		// episode file... We never silently drop a file from a plan."
		ep := episodeFor(p, f)
		if ep.Season < 0 {
			// No episode number is derivable. A date-based episode still
			// has a usable name — Plex accepts "Show - 2024-03-14" — so a
			// plan is built and flagged for review rather than refused.
			// Anything else has nothing to file under, and emitting E00
			// would invent an episode that exists in no agent's table.
			if p.AirDate == "" {
				res.NeedsReview = true
				res.ReviewReason = "no episode number could be derived for " + path.Base(f.RelPath)
				return nil, fmt.Errorf("layout: no episode number for %s", f.RelPath)
			}
			ep = episode{Season: 0, Marker: p.AirDate}
		}

		season := fmt.Sprintf("Season %02d", ep.Season)
		stem := fmt.Sprintf("%s - %s", p.Title, ep.Marker)
		name, tw := TruncateComponent(stem, f.Ext, "", lib.Opts)
		rel, abs, w, err := join(lib, series, season, name)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: append(w, tw...)})
	}
	return out, nil
}

// episode is one file's resolved position. Season is -1 when unresolvable.
type episode struct {
	Season int
	Marker string
}

// episodeFor resolves a single file, preferring the file's own name and
// falling back to the torrent-level parse for a single-episode payload.
func episodeFor(p media.Parsed, f media.SourceFile) episode {
	base := strings.TrimSuffix(path.Base(f.RelPath), f.Ext)
	if info := release.Parse(base); info.IsTV && info.HasSeason && len(info.Episodes) > 0 {
		return episode{Season: info.Season, Marker: marker(info.Season, info.Episodes)}
	}

	// The file name carried nothing. Fall back to the torrent-level parse,
	// but ONLY when it names a specific episode: a season-pack parse has a
	// season and no episode, and using it here is what produced E00.
	if p.Episode > 0 || (p.Season > 0 && p.EpisodeEnd > 0) {
		eps := []int{p.Episode}
		if p.EpisodeEnd > p.Episode {
			for e := p.Episode + 1; e <= p.EpisodeEnd; e++ {
				eps = append(eps, e)
			}
		}
		return episode{Season: p.Season, Marker: marker(p.Season, eps)}
	}
	return episode{Season: -1}
}

func marker(season int, eps []int) string {
	if len(eps) > 1 {
		return fmt.Sprintf("S%02dE%02d-E%02d", season, eps[0], eps[len(eps)-1])
	}
	return fmt.Sprintf("S%02dE%02d", season, eps[0])
}

// buildVerbatim places files under one folder without renaming any of them.
//
// This is how music and audiobooks are handled, and the rule is absolute:
// renaming cannot improve Navidrome's discovery (it reads tags, not paths)
// and it breaks .cue sheets, .log files, .m3u playlists, gapless references
// and the seeding torrent's file list. Subfolder structure — CD1/, Disc 1/ —
// is preserved because both servers rely on it.
func buildVerbatim(p media.Parsed, files []media.SourceFile, lib Library, folder string) ([]PlannedFile, error) {
	if strings.TrimSpace(folder) == "" {
		return nil, fmt.Errorf("layout: cannot derive a destination folder")
	}
	// The folder is a multi-level path ({Artist}/{Album}, or
	// {Author}/{Series}/{Book}), so it must be split before sanitisation:
	// SanitizeComponent turns a '/' inside a single component into '-',
	// which would flatten "Boards of Canada/Music Has the Right to
	// Children" into one directory named with a hyphen.
	var out []PlannedFile
	for _, f := range files {
		parts := strings.Split(folder, "/")
		parts = append(parts, payloadTail(f.RelPath, keepDiscDirs)...)
		rel, abs, w, err := join(lib, parts...)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: w})
	}
	return out, nil
}

// payloadTail returns the part of a source path that may be carried into
// the destination.
//
// Audiobookshelf groups at the deepest directory that DIRECTLY contains a
// media file, with exactly one exception: a single trailing directory
// matching /^(cd|dis[ck])\s*\d{1,3}$/i (server/utils/scandir.js). So the
// torrent's own subtree cannot be appended wholesale — every extra level
// shifts ABS's author/series/title slots by one, which mislabels the book
// rather than hiding it.
//
// keepDiscDirs preserves that one exception, because both ABS and
// Navidrome rely on it for multi-disc releases.
func payloadTail(rel string, keep func(string) bool) []string {
	segs := strings.Split(rel, "/")
	if len(segs) <= 1 {
		return segs
	}
	name := segs[len(segs)-1]
	// Walk backwards from the file, keeping only trailing disc directories.
	var kept []string
	for i := len(segs) - 2; i >= 0; i-- {
		if keep(segs[i]) {
			kept = append([]string{segs[i]}, kept...)
			continue
		}
		break
	}
	return append(kept, name)
}

var reDiscDir = regexp.MustCompile(`(?i)^(cd|dis[ck])\s*\d{1,3}$`)

func keepDiscDirs(seg string) bool { return reDiscDir.MatchString(seg) }

func musicFolder(p media.Parsed) string {
	artist := p.Artist
	if artist == "" {
		artist = p.Title
	}
	album := p.Album
	if album == "" {
		album = p.Title
	}
	if p.Year > 0 {
		return path.Join(artist, fmt.Sprintf("%s (%d)", album, p.Year))
	}
	return path.Join(artist, album)
}

// audiobookFolder follows Audiobookshelf's folder grammar, which IS its
// metadata parser — so the job is to not destroy information rather than to
// reproduce it.
func audiobookFolder(p media.Parsed) string {
	author := p.Author
	if author == "" {
		// Honest degradation beats clever guessing: Audiobookshelf still
		// ingests this and the user fixes metadata in ABS, which is a
		// better tool for that job than Orphanarr will ever be.
		author = "Unknown Author"
	}
	book := p.Title
	if p.Volume > 0 && p.Year > 0 {
		book = fmt.Sprintf("%d - %d - %s", p.Volume, p.Year, p.Title)
	} else if p.Year > 0 {
		book = fmt.Sprintf("%d - %s", p.Year, p.Title)
	}
	if p.Narrator != "" {
		book += fmt.Sprintf(" {%s}", p.Narrator)
	}
	if p.Series != "" {
		return path.Join(author, p.Series, book)
	}
	return path.Join(author, book)
}

// buildComic emits {root}/{Series}/<books>, which is what Komga actually
// reads: a Series is created for a directory that DIRECTLY contains book
// files (FileSystemScanner.kt postVisitDirectory), not for every subfolder
// at any depth as its documentation claims.
func buildComic(p media.Parsed, files []media.SourceFile, lib Library) ([]PlannedFile, error) {
	series := p.Series
	if series == "" {
		series = p.Title
	}
	folder := series
	if p.Volume == 0 && p.Issue == "" && lib.OneshotsDir != "" {
		folder = lib.OneshotsDir
	}
	if err := checkOneshots(lib); err != nil {
		return nil, err
	}

	var out []PlannedFile
	for _, f := range files {
		base := path.Base(f.RelPath)
		name, tw := TruncateComponent(strings.TrimSuffix(base, f.Ext), f.Ext, "", lib.Opts)
		rel, abs, w, err := join(lib, folder, name)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: append(w, tw...)})
	}
	return out, nil
}

// buildEbook is buildComic at a different root, because it is the same
// server. Three branches (DESIGN §5.7).
func buildEbook(p media.Parsed, files []media.SourceFile, lib Library) ([]PlannedFile, error) {
	if err := checkOneshots(lib); err != nil {
		return nil, err
	}

	var out []PlannedFile
	for _, f := range files {
		var folder, stem string
		switch {
		case p.Series != "":
			folder = p.Series
			if p.Volume > 0 {
				// Zero-padded. Komga's own comparator is natural, so it
				// does not need this — every other OPDS client does.
				stem = fmt.Sprintf("%s %02d - %s", p.Series, p.Volume, p.Title)
			} else {
				stem = fmt.Sprintf("%s - %s", p.Series, p.Title)
			}
		case lib.OneshotsDir != "":
			folder = lib.OneshotsDir
			stem = p.Title
			if p.Author != "" {
				stem = fmt.Sprintf("%s - %s", p.Author, p.Title)
			}
		default:
			// Branch 3. Without a CONFIRMED one-shots directory, branch 2
			// would emit a folder Komga does not recognise, collapsing
			// every standalone book into one series.
			folder = p.Title
			stem = p.Title
		}
		name, tw := TruncateComponent(stem, f.Ext, "", lib.Opts)
		rel, abs, w, err := join(lib, folder, name)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: append(w, tw...)})
	}
	return out, nil
}

// checkOneshots refuses a one-shots name that occurs in the library root's
// own absolute path.
//
// Komga matches one-shots with dir.pathString.contains(oneshotsDir, true) —
// a case-insensitive raw substring on the ABSOLUTE path — so a one-shots
// name of "books" under /data/media/ebooks turns the entire library into
// one-shots.
func checkOneshots(lib Library) error {
	if lib.OneshotsDir == "" {
		return nil
	}
	if strings.Contains(strings.ToLower(lib.Root), strings.ToLower(lib.OneshotsDir)) {
		return fmt.Errorf("layout: one-shots directory %q occurs in the library root %q; "+
			"Komga matches it as a case-insensitive substring of the absolute path, "+
			"which would make every book in this library a one-shot",
			lib.OneshotsDir, lib.Root)
	}
	return nil
}

// buildROM emits {root}/{platform}/<file>, which is RomM's model. A
// multi-disc game is a FOLDER of files, not loose siblings.
func buildROM(p media.Parsed, files []media.SourceFile, lib Library) ([]PlannedFile, error) {
	if p.Platform == "" {
		return nil, fmt.Errorf("layout: RomM requires a platform and none was determined")
	}
	multi := len(files) > 1

	var out []PlannedFile
	for _, f := range files {
		base := path.Base(f.RelPath)
		name, tw := TruncateComponent(strings.TrimSuffix(base, f.Ext), f.Ext, "", lib.Opts)

		// RomM's library is {base}/roms/{platform}/, never
		// {base}/{platform}/. ROMS_FOLDER_NAME defaults to "roms" and
		// get_platform_fs_structure() builds "roms/{slug}"; without that
		// level RomM finds nothing, bootstraps an empty roms/ directory
		// and logs a warning the user never sees.
		parts := []string{romsDir, p.Platform}
		if multi {
			parts = append(parts, p.Title)
		}
		parts = append(parts, name)

		rel, abs, w, err := join(lib, parts...)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{Src: f, DstRel: rel, Dst: abs, Warnings: append(w, tw...)})
	}
	return out, nil
}

// romsDir is RomM's ROMS_FOLDER_NAME default. The library root the user
// configures is RomM's LIBRARY_BASE_PATH, and this level sits beneath it.
const romsDir = "roms"

func largest(files []media.SourceFile) media.SourceFile {
	var best media.SourceFile
	for _, f := range files {
		if f.Size > best.Size {
			best = f
		}
	}
	return best
}

// suffixFor distinguishes a companion file from the main payload, e.g. an
// English subtitle beside the feature.
func suffixFor(f, main media.SourceFile) string {
	base := strings.TrimSuffix(path.Base(f.RelPath), f.Ext)
	mainBase := strings.TrimSuffix(path.Base(main.RelPath), main.Ext)
	if extra := strings.TrimPrefix(base, mainBase); extra != base && extra != "" {
		return extra
	}
	if base != "" {
		return "." + base
	}
	return ""
}

// SortPlanned orders placements deterministically. A plan the user reviews
// must look the same every time it is rendered.
func SortPlanned(files []PlannedFile) {
	sort.SliceStable(files, func(i, j int) bool { return files[i].Dst < files[j].Dst })
}
