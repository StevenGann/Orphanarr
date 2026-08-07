package classify

import "github.com/StevenGann/Orphanarr/internal/media"

// family groups extensions by what they can possibly be. It is coarser than
// media.Type on purpose: .flac could be music or an audiobook, and .mkv
// could be a film or an episode, so the family narrows the field and the
// later signals decide.
type family int

const (
	famOther family = iota
	famVideo
	famAudio
	famBook  // reflowable ebook formats
	famComic // comic archives
	famROM   // console images with an unambiguous platform
	famPDF   // neither book nor comic on its own — see the tie-break
	famImage // cover art, scanned pages
	famSubtitle
	famSidecar // nfo, sfv, opf, cue, m3u — metadata about the payload
	famArchive // rar/7z sets: the payload is invisible until extraction
	famSoftware
)

// primary reports whether a family can carry the identity of an orphan.
// Sidecars and cover art travel with the payload but never decide what it is
// — counting them as evidence is how a folder of subtitles becomes a film.
func (f family) primary() bool {
	switch f {
	case famVideo, famAudio, famBook, famComic, famROM, famPDF:
		return true
	}
	return false
}

var extFamily = map[string]family{}

// romPlatform maps an extension to a RomM platform slug where — and only
// where — the extension implies exactly one platform.
//
// Absent by design: .iso spans GameCube, Wii, PS2, PSP, Xbox and PC
// installers, and .bin spans PS1 tracks, BIOS images and raw dumps. Both
// appear in the corpus as traps precisely because guessing a platform from
// them files someone's Linux ISO into a games library.
// Every value is a RomM UniversalPlatformSlug. RomM matches the folder name
// against that enum, so a slug that is merely plausible produces a platform
// with no metadata and an info-level log — no error the user will ever see.
//
// Six of these were wrong until 2026-08-07, and one was a trap worth
// naming: the .ngc EXTENSION is Neo Geo Pocket Color, while the SLUG "ngc"
// is GameCube. The obvious correction of "ngpc" to "ngc" would file Neo Geo
// Pocket games into a GameCube library.
var romPlatform = map[string]string{
	".sfc": "snes", ".smc": "snes",
	".z64": "n64", ".n64": "n64", ".v64": "n64",
	".nes": "nes", ".fds": "fds",
	".gb": "gb", ".gbc": "gbc", ".gba": "gba",
	".sms": "sms", ".gg": "gamegear",
	".nds": "nds", ".3ds": "3ds",
	".pce": "tg16", ".32x": "sega32",
	".a26": "atari2600", ".a78": "atari7800", ".lnx": "lynx",
	".ws": "wonderswan", ".wsc": "wonderswan-color",
	".ngp": "neo-geo-pocket", ".ngc": "neo-geo-pocket-color",
	".vb": "virtualboy",
}

// ambiguousROMExt are extensions that appear on ROMs but are not evidence of
// one on their own. .md is also Markdown; .iso is also a Linux distribution.
var ambiguousROMExt = map[string]string{
	".md": "genesis", ".smd": "genesis", ".gen": "genesis",
	".bin": "", ".iso": "", ".cue": "", ".chd": "",
}

func init() {
	reg := func(f family, exts ...string) {
		for _, e := range exts {
			extFamily[e] = f
		}
	}
	reg(famVideo, ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".m2ts", ".mov", ".wmv",
		".mpg", ".mpeg", ".flv", ".webm", ".vob", ".divx", ".ogv")
	reg(famAudio, ".flac", ".mp3", ".m4a", ".m4b", ".ogg", ".opus", ".wav",
		".aac", ".wma", ".ape", ".alac", ".aiff", ".dsf", ".wv", ".mka")
	reg(famBook, ".epub", ".azw3", ".azw", ".mobi", ".fb2", ".kepub", ".lit", ".pdb")
	reg(famComic, ".cbz", ".cbr", ".cb7", ".cbt", ".cba")
	reg(famPDF, ".pdf")
	reg(famImage, ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff")
	reg(famSubtitle, ".srt", ".sub", ".idx", ".ass", ".ssa", ".vtt", ".sup")
	reg(famSidecar, ".nfo", ".sfv", ".opf", ".m3u", ".m3u8", ".log", ".txt",
		".md5", ".sha1", ".json", ".xml", ".torrent", ".url", ".jpg_")
	reg(famArchive, ".rar", ".r00", ".r01", ".r02", ".r03", ".7z", ".tar",
		".gz", ".bz2", ".part1", ".001")
	reg(famSoftware, ".exe", ".msi", ".dll", ".dmg", ".apk", ".deb", ".rpm",
		".appimage", ".wad", ".pak", ".sh", ".bat")

	for e := range romPlatform {
		extFamily[e] = famROM
	}
}

// familyOf classifies one extension.
func familyOf(ext string) family {
	if f, ok := extFamily[ext]; ok {
		return f
	}
	return famOther
}

// bookFormatFor reports the media type a book-family extension implies. Both
// Komga (comics, PDFs, EPUB) and a reflowable reader claim some of these, so
// the caller still applies the PDF tie-break.
func bookTypeFor(ext string) media.Type {
	switch familyOf(ext) {
	case famComic:
		return media.Comic
	case famBook:
		return media.Ebook
	}
	return media.Unknown
}
