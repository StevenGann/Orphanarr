package layout

import (
	"strings"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/media"
)

func file(rel, ext string, size int64) media.SourceFile {
	return media.SourceFile{RelPath: rel, Ext: ext, Size: size}
}

func firstDst(t *testing.T, res Result) string {
	t.Helper()
	for _, f := range res.Files {
		if !f.Skip {
			return f.Dst
		}
	}
	t.Fatal("no non-skipped placement")
	return ""
}

func TestMovieLayout(t *testing.T) {
	lib := Library{Type: media.Movie, Root: "/data/media/movies"}
	res, err := Build(media.Movie,
		media.Parsed{Title: "The Matrix", Year: 1999},
		[]media.SourceFile{file("the.matrix.1999.mkv", ".mkv", 8<<30)}, lib)
	if err != nil {
		t.Fatal(err)
	}
	want := "/data/media/movies/The Matrix (1999)/The Matrix (1999).mkv"
	if got := firstDst(t, res); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Plex's {edition-...} tag needs Plex Pass and is visible noise in Jellyfin,
// so it ships off (BRIEF Q22). The parse still recovers the edition — this
// asserts the emission is what is gated, not the parse.
func TestMovieEditionTagIsOffByDefault(t *testing.T) {
	p := media.Parsed{Title: "Blade Runner", Year: 1982, Edition: "Final Cut"}
	src := []media.SourceFile{file("br.mkv", ".mkv", 8<<30)}

	off, _ := Build(media.Movie, p, src, Library{Root: "/m"})
	if strings.Contains(firstDst(t, off), "edition-") {
		t.Errorf("edition tag emitted by default: %q", firstDst(t, off))
	}

	on, _ := Build(media.Movie, p, src, Library{Root: "/m", EmitEditionTags: true})
	if !strings.Contains(firstDst(t, on), "{edition-Final Cut}") {
		t.Errorf("edition tag not emitted when enabled: %q", firstDst(t, on))
	}
}

func TestTVLayout(t *testing.T) {
	lib := Library{Type: media.TV, Root: "/data/media/tv"}
	res, err := Build(media.TV,
		media.Parsed{Title: "Severance", Season: 1, Episode: 3},
		[]media.SourceFile{file("s01e03.mkv", ".mkv", 4<<30)}, lib)
	if err != nil {
		t.Fatal(err)
	}
	want := "/data/media/tv/Severance/Season 01/Severance - S01E03.mkv"
	if got := firstDst(t, res); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A date-based episode yields no season number, and Plex files dailies under
// a real season the date does not supply. Guessing puts the episode in a
// season the series may not have, so this must route to review.
func TestTVDateBasedRoutesToReview(t *testing.T) {
	res, err := Build(media.TV,
		media.Parsed{Title: "The Daily Show", AirDate: "2024-03-14"},
		[]media.SourceFile{file("ep.mkv", ".mkv", 2<<30)},
		Library{Root: "/tv"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsReview {
		t.Fatal("date-based TV must route to review, not auto-file")
	}
}

func TestAnimeAbsoluteRoutesToReview(t *testing.T) {
	res, _ := Build(media.TV,
		media.Parsed{Title: "Frieren", Absolute: 12},
		[]media.SourceFile{file("e12.mkv", ".mkv", 2<<30)},
		Library{Root: "/tv"})
	if !res.NeedsReview {
		t.Fatal("absolute numbering must route to review")
	}
}

// Music is placed fully verbatim: no track is ever renamed. Renaming cannot
// improve Navidrome's discovery — it reads tags, not paths — and it breaks
// .cue sheets, .m3u playlists, gapless references and the seeding torrent's
// file list.
func TestMusicIsVerbatimAndPreservesDiscFolders(t *testing.T) {
	res, err := Build(media.Music,
		media.Parsed{Artist: "Boards of Canada", Album: "Music Has the Right to Children", Year: 1998},
		[]media.SourceFile{
			file("01 - Wildlife Analysis.flac", ".flac", 40<<20),
			file("CD2/01 - Track.flac", ".flac", 40<<20),
			file("cover.jpg", ".jpg", 1<<20),
		},
		Library{Type: media.Music, Root: "/data/media/music"})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, f := range res.Files {
		got = append(got, f.Dst)
	}
	joined := strings.Join(got, "\n")

	for _, want := range []string{
		"/data/media/music/Boards of Canada/Music Has the Right to Children (1998)/01 - Wildlife Analysis.flac",
		"/data/media/music/Boards of Canada/Music Has the Right to Children (1998)/CD2/01 - Track.flac",
		"/data/media/music/Boards of Canada/Music Has the Right to Children (1998)/cover.jpg",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q\ngot:\n%s", want, joined)
		}
	}
}

// Audiobookshelf's folder grammar IS its metadata parser, so an unknown
// author degrades to a named placeholder rather than to a guess. ABS still
// ingests it and the user fixes metadata there — a better tool for that job
// than Orphanarr will ever be.
func TestAudiobookUnknownAuthorDegradesHonestly(t *testing.T) {
	res, err := Build(media.Audiobook,
		media.Parsed{Title: "Some Book"},
		[]media.SourceFile{file("book.m4b", ".m4b", 400<<20)},
		Library{Root: "/ab"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstDst(t, res), "Unknown Author") {
		t.Errorf("expected an explicit Unknown Author folder, got %q", firstDst(t, res))
	}
}

// Komga creates a Series only for a directory that DIRECTLY contains books.
// Without a confirmed one-shots directory, branch 2 would emit a folder
// Komga does not recognise and collapse every standalone book into one
// series — so branch 3 uses the title as its own folder.
func TestEbookWithoutOneshotsDirUsesTitleFolder(t *testing.T) {
	res, err := Build(media.Ebook,
		media.Parsed{Title: "Project Hail Mary", Author: "Andy Weir"},
		[]media.SourceFile{file("phm.epub", ".epub", 2<<20)},
		Library{Root: "/data/media/ebooks"})
	if err != nil {
		t.Fatal(err)
	}
	want := "/data/media/ebooks/Project Hail Mary/Project Hail Mary.epub"
	if got := firstDst(t, res); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEbookSeriesIsZeroPadded(t *testing.T) {
	res, _ := Build(media.Ebook,
		media.Parsed{Title: "Equal Rites", Series: "Discworld", Volume: 3},
		[]media.SourceFile{file("er.epub", ".epub", 2<<20)},
		Library{Root: "/eb"})
	if got := firstDst(t, res); !strings.Contains(got, "Discworld 03 - Equal Rites") {
		t.Errorf("expected a zero-padded volume, got %q", got)
	}
}

// Komga matches one-shots as a case-insensitive raw substring of the
// ABSOLUTE path, so a one-shots name that occurs in the library root turns
// the entire library into one-shots.
func TestOneshotsDirInLibraryRootIsRefused(t *testing.T) {
	_, err := Build(media.Ebook,
		media.Parsed{Title: "X", Author: "Y"},
		[]media.SourceFile{file("x.epub", ".epub", 1<<20)},
		Library{Root: "/data/media/ebooks", OneshotsDir: "books"})
	if err == nil {
		t.Fatal("a one-shots name occurring in the library root must be refused")
	}
	if !strings.Contains(err.Error(), "one-shot") {
		t.Errorf("error should explain the Komga substring match, got: %v", err)
	}
}

// RomM's layout requires a platform. Guessing one from .iso or .bin files a
// user's Linux distribution into a games library.
func TestROMWithoutPlatformIsRefused(t *testing.T) {
	_, err := Build(media.ROM,
		media.Parsed{Title: "Metroid Prime"},
		[]media.SourceFile{file("mp.iso", ".iso", 1<<30)},
		Library{Root: "/roms"})
	if err == nil {
		t.Fatal("a ROM with no determined platform must be refused, not guessed")
	}
}

func TestROMMultiDiscBecomesAFolder(t *testing.T) {
	res, err := Build(media.ROM,
		media.Parsed{Title: "Final Fantasy VII", Platform: "psx"},
		[]media.SourceFile{
			file("FF7 (Disc 1).cue", ".cue", 1<<10),
			file("FF7 (Disc 1).bin", ".bin", 700<<20),
		},
		Library{Root: "/roms"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if !strings.HasPrefix(f.Dst, "/roms/psx/Final Fantasy VII/") {
			t.Errorf("multi-disc game must be a folder of files, got %q", f.Dst)
		}
	}
}

// The per-library ingest rule refuses rather than warns. A warning is not a
// gate, so a warned item auto-files into invisibility — which is what the
// old .fb2 plan-warning did.
func TestUningestibleFormatsAreRefusedNotWarned(t *testing.T) {
	lib := Library{
		Type:         media.Ebook,
		Root:         "/eb",
		AcceptedExts: map[string]bool{".epub": true, ".pdf": true},
	}
	res, err := Build(media.Ebook,
		media.Parsed{Title: "Book"},
		[]media.SourceFile{file("book.mobi", ".mobi", 1<<20)}, lib)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsReview {
		t.Fatal("a payload with no ingestible files must route to review")
	}
	found := false
	for _, f := range res.Files {
		if f.Skip && f.SkipReason == "SKIP_UNSUPPORTED_FORMAT" {
			found = true
		}
	}
	if !found {
		t.Error("expected SKIP_UNSUPPORTED_FORMAT, not a plan warning")
	}
}

// No layout may ever emit a path outside its library root, whatever the
// parse produced. This is I6 asserted at the layer that constructs paths.
func TestNoLayoutEscapesTheLibraryRoot(t *testing.T) {
	hostile := media.Parsed{
		Title: "../../etc", Series: "../..", Artist: "..", Album: "../x",
		Author: "../../root", Platform: "../psx", Volume: 1,
	}
	src := []media.SourceFile{file("../../../etc/passwd", "", 1)}

	for _, ty := range media.AllTypes {
		lib := Library{Type: ty, Root: "/data/media/x"}
		res, err := Build(ty, hostile, src, lib)
		if err != nil {
			continue // refusing is a correct outcome
		}
		for _, f := range res.Files {
			if f.Skip {
				continue
			}
			if !strings.HasPrefix(f.Dst, "/data/media/x/") {
				t.Errorf("%s: escaped the library root: %q", ty, f.Dst)
			}
		}
	}
}
