package classify

import (
	"strings"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/media"
)

// The classifier must populate the type-specific fields the layout engine
// needs, not just the type.
//
// This is the gap all four round-03 reviewers found independently: toParsed
// copied nine fields and left eight at their zero values, so buildROM
// refused EVERY rom outright and comics, ebooks, music and audiobooks all
// filed to wrong paths with no error. A classification that names the type
// and nothing else is not usable output.
func TestEnrichPopulatesTheFieldsLayoutNeeds(t *testing.T) {
	rules := DefaultRules()

	cases := []struct {
		name  string
		fs    media.FileSet
		want  media.Type
		check func(t *testing.T, p media.Parsed)
	}{
		{
			name: "rom platform from an unambiguous extension",
			fs: media.FileSet{Name: "Super Mario World (USA)", Files: []media.SourceFile{
				{RelPath: "Super Mario World (USA).sfc", Ext: ".sfc", Size: 512 << 10},
			}},
			want: media.ROM,
			check: func(t *testing.T, p media.Parsed) {
				if p.Platform != "snes" {
					t.Errorf("Platform = %q, want snes — RomM's layout REQUIRES a "+
						"platform and refuses without one", p.Platform)
				}
				if !strings.Contains(p.Title, "Super Mario World") {
					t.Errorf("Title = %q", p.Title)
				}
			},
		},
		{
			name: "rom platform from a parent directory",
			fs: media.FileSet{Name: "nes", Files: []media.SourceFile{
				{RelPath: "nes/Super Mario Bros. (World).nes", Ext: ".nes", Size: 40 << 10},
			}},
			want: media.ROM,
			check: func(t *testing.T, p media.Parsed) {
				if p.Platform != "nes" {
					t.Errorf("Platform = %q, want nes", p.Platform)
				}
			},
		},
		{
			name: "music artist and album",
			fs:   media.FileSet{Name: "Radiohead - OK Computer (1997) [FLAC]"},
			want: media.Music,
			check: func(t *testing.T, p media.Parsed) {
				if p.Artist != "Radiohead" {
					t.Errorf("Artist = %q, want Radiohead", p.Artist)
				}
				if p.Album != "OK Computer" {
					t.Errorf("Album = %q, want OK Computer", p.Album)
				}
				if p.Year != 1997 {
					t.Errorf("Year = %d, want 1997", p.Year)
				}
			},
		},
		{
			name: "VA compilation groups under Various Artists",
			fs:   media.FileSet{Name: "VA-Now_Thats_What_I_Call_Music_100-2CD-FLAC-2018-MTD"},
			want: media.Music,
			check: func(t *testing.T, p media.Parsed) {
				// Navidrome groups on ALBUMARTIST; a compilation whose
				// tracks each carry a different artist only coheres here.
				if p.Artist != "Various Artists" {
					t.Errorf("Artist = %q, want Various Artists", p.Artist)
				}
			},
		},
		{
			name: "comic series excludes the volume token",
			fs: media.FileSet{Name: "Saga v01 (2012) (Digital) (Zone-Empire)", Files: []media.SourceFile{
				{RelPath: "Saga v01 (2012) (Digital) (Zone-Empire).cbz", Ext: ".cbz", Size: 30 << 20},
			}},
			want: media.Comic,
			check: func(t *testing.T, p media.Parsed) {
				// Komga creates one Series per directory that directly
				// contains books, so a folder named "Saga v01" makes a
				// 34-volume run into 34 separate series.
				if p.Series != "Saga" {
					t.Errorf("Series = %q, want Saga — the volume token must not "+
						"survive into the series name", p.Series)
				}
				if p.Volume != 1 {
					t.Errorf("Volume = %d, want 1", p.Volume)
				}
			},
		},
		{
			name: "audiobook author and narrator",
			fs: media.FileSet{Name: "Andy Weir - The Martian (2014) {R.C. Bray}", Files: []media.SourceFile{
				{RelPath: "The Martian.m4b", Ext: ".m4b", Size: 400 << 20},
			}},
			want: media.Audiobook,
			check: func(t *testing.T, p media.Parsed) {
				if p.Author != "Andy Weir" {
					t.Errorf("Author = %q, want Andy Weir — without it every book "+
						"files under 'Unknown Author'", p.Author)
				}
				if p.Narrator != "R.C. Bray" {
					t.Errorf("Narrator = %q, want R.C. Bray", p.Narrator)
				}
			},
		},
		{
			name: "ebook author and title",
			fs: media.FileSet{Name: "Neal Stephenson - Snow Crash", Files: []media.SourceFile{
				{RelPath: "Neal Stephenson - Snow Crash.epub", Ext: ".epub", Size: 2 << 20},
			}},
			want: media.Ebook,
			check: func(t *testing.T, p media.Parsed) {
				if p.Author != "Neal Stephenson" {
					t.Errorf("Author = %q, want Neal Stephenson", p.Author)
				}
				if p.Title != "Snow Crash" {
					t.Errorf("Title = %q, want Snow Crash — collapsing the title to "+
						"the author renames the file and loses the book", p.Title)
				}
			},
		},
		{
			name: "'Last, First' author is normalised",
			fs: media.FileSet{Name: "Sanderson, Brandon - Mistborn 01 - The Final Empire",
				Files: []media.SourceFile{
					{RelPath: "Sanderson, Brandon - Mistborn 01 - The Final Empire.epub",
						Ext: ".epub", Size: 2 << 20},
				}},
			want: media.Ebook,
			check: func(t *testing.T, p media.Parsed) {
				if p.Author != "Brandon Sanderson" {
					t.Errorf("Author = %q, want Brandon Sanderson", p.Author)
				}
				if p.Series != "Mistborn" || p.Volume != 1 {
					t.Errorf("Series/Volume = %q/%d, want Mistborn/1", p.Series, p.Volume)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, p := Classify(c.fs, rules)
			if cl.Type != c.want {
				t.Fatalf("type = %s, want %s (signals: %v)", cl.Type, c.want, cl.Signals)
			}
			c.check(t, p)
		})
	}
}

// A genuinely ambiguous extension must yield NO platform, so the layout
// engine refuses and the item is surfaced for review.
//
// Guessing here files someone's Linux distribution image into a games
// library, which is the corpus case .iso exists to trap.
func TestAmbiguousExtensionYieldsNoPlatform(t *testing.T) {
	fs := media.FileSet{Name: "Metroid Prime (USA)", Files: []media.SourceFile{
		{RelPath: "Metroid Prime (USA).iso", Ext: ".iso", Size: 1 << 30},
	}}
	cl, p := Classify(fs, DefaultRules())
	if cl.Type != media.ROM {
		t.Fatalf("type = %s, want rom", cl.Type)
	}
	if p.Platform != "" {
		t.Errorf("Platform = %q; .iso spans six consoles and Linux, so it must "+
			"yield nothing and route to review", p.Platform)
	}
}

// Every ROM slug must be a real RomM UniversalPlatformSlug. RomM matches the
// folder name against that enum and a merely-plausible slug produces a
// platform with no metadata and an info-level log the user never sees.
func TestROMSlugsAreRomMSlugs(t *testing.T) {
	// Spot-check the six that were wrong, plus the one that is a trap: the
	// .ngc EXTENSION is Neo Geo Pocket Color, while the SLUG "ngc" is
	// GameCube.
	want := map[string]string{
		".32x": "sega32", ".lnx": "lynx", ".ngp": "neo-geo-pocket",
		".ngc": "neo-geo-pocket-color", ".pce": "tg16", ".wsc": "wonderswan-color",
	}
	for ext, slug := range want {
		if got := romPlatform[ext]; got != slug {
			t.Errorf("romPlatform[%q] = %q, want %q", ext, got, slug)
		}
	}
	if romPlatform[".ngc"] == "ngc" {
		t.Error(".ngc maps to the GameCube slug; Neo Geo Pocket games would file " +
			"into a GameCube library")
	}
}
