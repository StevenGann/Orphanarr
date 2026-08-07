package layout

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Sanitization is not optional and is easy to get wrong (DESIGN §5.8). The
// rules below exist because a torrent supplies these names and a torrent is
// hostile input.

var (
	// ErrTraversal is a path component that escapes the destination root.
	ErrTraversal = errors.New("layout: path component escapes the destination root")
	// ErrNulByte is a NUL in a name. NUL terminates C strings, so a
	// runtime that passes it through truncates silently at the syscall.
	ErrNulByte = errors.New("layout: NUL byte in name")
	// ErrEmptyName is a component that sanitised away to nothing.
	ErrEmptyName = errors.New("layout: name is empty after sanitisation")
)

// PartialSuffix is appended to a destination while a copy is in flight.
//
// The trailing ".tmp" is load-bearing rather than cosmetic. RomM's scanner
// is an EXCLUSION list, alone among the six target servers, so a partial
// without a recognised-junk extension gets indexed and hashed — leaving a
// row for a vanished path plus a duplicate for the real ROM. "tmp" is
// already in RomM's default exclusion list.
const PartialSuffix = ".orphanarr-partial.tmp"

// NameBudget is the per-component byte budget.
//
// NAME_MAX is 255 BYTES, not characters (#C13: 132 non-ASCII characters can
// exceed it). The budget is 255 minus the partial suffix, unconditionally —
// layout is a pure function that runs before the executor attempts anything,
// so it cannot know whether a given placement will be a copy, and after the
// 2026-08-07 amendment copy is the primary path anyway.
//
// Derived rather than hard-coded: a previous version pinned 237 for an
// 18-byte suffix and had to be re-pinned when the suffix changed.
var NameBudget = 255 - len(PartialSuffix)

// reservedStems are illegal as filenames on Windows and SMB shares, with or
// without an extension. A Linux-only test suite will not catch these, and a
// large share of real libraries live on SMB.
var reservedStems = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// nonPOSIXIllegal are legal on ext4 (proven: #C14) and illegal on
// SMB/NTFS/exFAT.
const nonPOSIXIllegal = `:"<>|?*\`

var reControl = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// Options controls how strict sanitisation is for a given destination. The
// answer is per-destination because it depends on the filesystem the
// library root actually lives on (DESIGN §5.8).
type Options struct {
	// StrictNames applies the SMB/NTFS/exFAT rules. Auto-detected from
	// /proc/mounts during setup, with the user's answer winning.
	StrictNames bool
	// Budget overrides NameBudget. Zero means use the default.
	Budget int
}

// Warning records a change sanitisation made, so it can be shown in the plan
// before execution rather than discovered afterwards.
type Warning struct {
	Kind   string
	Before string
	After  string
}

func (w Warning) String() string {
	return fmt.Sprintf("%s: %q -> %q", w.Kind, w.Before, w.After)
}

// SanitizeComponent cleans one path component. It never returns a value
// containing a separator, and it refuses rather than repairs anything that
// could redirect a write.
func SanitizeComponent(s string, opt Options) (string, []Warning, error) {
	var warns []Warning

	if strings.ContainsRune(s, 0) {
		return "", nil, fmt.Errorf("%w: %q", ErrNulByte, s)
	}

	// Refuse traversal outright. Repairing ".." is how a "sanitised" name
	// still ends up writing to /etc.
	if s == "." || s == ".." {
		return "", nil, fmt.Errorf("%w: %q", ErrTraversal, s)
	}

	orig := s

	// Unicode NFC. Preserve everything else — people have Japanese,
	// Cyrillic and accented libraries, and ASCII-folding them is an insult
	// rather than a feature.
	s = norm.NFC.String(s)

	// Separators can never survive inside a component: a torrent-supplied
	// name containing '/' would otherwise create directories.
	s = strings.ReplaceAll(s, "/", "-")

	// Control characters, including the embedded newline that corrupts
	// every line-oriented log, shell pipeline and CSV export.
	if reControl.MatchString(s) {
		s = reControl.ReplaceAllString(s, "")
		warns = append(warns, Warning{"control-chars", orig, s})
	}

	if opt.StrictNames {
		if strings.ContainsAny(s, nonPOSIXIllegal) {
			before := s
			s = strings.Map(func(r rune) rune {
				if strings.ContainsRune(nonPOSIXIllegal, r) {
					return '_'
				}
				return r
			}, s)
			warns = append(warns, Warning{"non-posix-chars", before, s})
		}
	}

	// Trailing dots and spaces are legal on POSIX and silently stripped or
	// rejected by SMB, which creates phantom path mismatches on the next
	// run — the tool then cannot find what it just wrote.
	if trimmed := strings.TrimRight(s, ". "); trimmed != s {
		warns = append(warns, Warning{"trailing-dot-space", s, trimmed})
		s = trimmed
	}
	s = strings.TrimLeft(s, " ")

	// Reserved device stems, with or without an extension.
	stem := s
	if i := strings.IndexByte(stem, '.'); i > 0 {
		stem = stem[:i]
	}
	if reservedStems[strings.ToLower(stem)] {
		before := s
		s = "_" + s
		warns = append(warns, Warning{"reserved-name", before, s})
	}

	if strings.TrimSpace(s) == "" {
		return "", warns, fmt.Errorf("%w: %q", ErrEmptyName, orig)
	}
	return s, warns, nil
}

// SanitizeRelPath cleans a multi-component relative path as supplied by a
// download client's file manifest.
//
// This is the entry point for hostile input, and it refuses rather than
// repairs. Repairing "../../../../etc/cron.d/pwn" by mangling the
// separators yields a plausible-looking component that is still built from
// an attempt to escape, and "/etc/passwd" is worse: path.Join(root,
// "/etc/passwd") DISCARDS the root and returns /etc/passwd.
func SanitizeRelPath(rel string, opt Options) ([]string, []Warning, error) {
	if strings.ContainsRune(rel, 0) {
		return nil, nil, fmt.Errorf("%w: %q", ErrNulByte, rel)
	}
	if strings.HasPrefix(rel, "/") {
		return nil, nil, fmt.Errorf("%w: absolute path %q", ErrTraversal, rel)
	}

	var out []string
	var warns []Warning
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return nil, nil, fmt.Errorf("%w: %q contains %q", ErrTraversal, rel, seg)
		}
		s, w, err := SanitizeComponent(seg, opt)
		if err != nil {
			return nil, nil, err
		}
		warns = append(warns, w...)
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, warns, fmt.Errorf("%w: %q", ErrEmptyName, rel)
	}
	return out, warns, nil
}

// TruncateComponent enforces the byte budget while protecting the parts of a
// name a scanner matches on.
//
// The extension and the structural marker are both preserved: the episode
// code, the volume or issue number and the disc number are what the media
// server keys on, and truncating a long Cyrillic series title from the left
// would remove the marker and leave a file nothing can place.
func TruncateComponent(s, ext, marker string, opt Options) (string, []Warning) {
	budget := opt.Budget
	if budget <= 0 {
		budget = NameBudget
	}

	full := s + marker + ext
	if len(full) <= budget {
		return full, nil
	}

	// Everything except the title portion is protected, so the title takes
	// the whole cut.
	room := budget - len(marker) - len(ext)
	if room < 1 {
		// Pathological: the protected parts alone exceed the budget. Keep
		// the extension, which is what makes the file openable at all.
		room = 1
	}
	title := truncateUTF8(s, room)
	out := title + marker + ext
	return out, []Warning{{"truncated", full, out}}
}

// truncateUTF8 cuts to at most n bytes without splitting a rune. A byte
// slice that ends mid-sequence is not valid UTF-8 and some filesystems and
// most user interfaces render it as replacement characters.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	b := []byte(s)[:n]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	out := strings.TrimRight(string(b), " .")
	if out == "" {
		// Never return empty: an empty component would collapse the path.
		return "x"
	}
	return out
}

// IsSampleOrExtra reports whether a file is a sample or a bonus feature that
// must not be imported as the main payload. A "sample.mkv" filed as the
// feature is a 40 MB movie in the library and a support thread.
func IsSampleOrExtra(relPath string) bool {
	lower := strings.ToLower(relPath)
	for _, seg := range strings.Split(lower, "/") {
		switch seg {
		// Jellyfin documents 13 external-extras folders; Plex's set
		// overlaps but is not identical, so this is the UNION. "clips",
		// "theme-music" and "backdrops" were missing, and their absence
		// was not cosmetic: a file literally named theme.mp3 is
		// Jellyfin's theme-media convention and renaming it kills it,
		// while a renamed clip with no version label reads to Jellyfin
		// as a SEPARATE MOVIE.
		case "sample", "samples", "extras", "featurettes", "trailers",
			"behind the scenes", "deleted scenes", "interviews", "scenes",
			"shorts", "other", "proof", "clips", "theme-music", "backdrops",
			"specials", "extrafanart", "extrathumbs":
			return true
		}
	}
	base := lower
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	// A leading or trailing "sample" token, not merely the substring: a
	// film legitimately called "Sampler" must survive.
	for _, sep := range []string{".", "-", "_", " "} {
		if strings.HasPrefix(base, "sample"+sep) || strings.Contains(base, sep+"sample"+sep) ||
			strings.HasPrefix(base, "rarbg"+sep) {
			return true
		}
	}
	return base == "sample" || strings.HasPrefix(base, "sample.")
}

// hasUpper reports whether s contains an uppercase rune. Used by the
// case-sensitivity probe path.
func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}
