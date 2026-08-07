// Package pathmap translates a download client's paths into the paths this
// container can see.
//
// It is mandatory, not a convenience. qBittorrent reports paths in ITS
// namespace; Orphanarr sees a different mount layout, and an unmapped path
// must ABORT rather than be used — creating directories at a bogus location
// is how a tool "does nothing" for two years while filling a stray folder.
package pathmap

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Rule maps one remote prefix to one local prefix.
type Rule struct {
	Remote string
	Local  string
}

// Mapper applies rules longest-prefix-first.
type Mapper struct {
	rules []Rule
}

// ErrUnmapped is returned when no rule covers a path. Callers must treat it
// as a refusal, never as "use the path as-is".
type ErrUnmapped struct{ Path string }

func (e *ErrUnmapped) Error() string {
	return fmt.Sprintf("pathmap: no mapping covers %q; refusing to guess a local path", e.Path)
}

// New sorts rules so the most specific wins. Without this, a rule for
// /downloads shadows a more specific one for /downloads/movies purely by
// declaration order, which is a configuration bug the user cannot see.
func New(rules []Rule) *Mapper {
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].Remote) > len(cp[j].Remote)
	})
	return &Mapper{rules: cp}
}

// Identity returns a mapper that passes paths through unchanged.
//
// This is the common case under BRIEF §5 A3: the container mounts the
// client's download folders, usually at the path the client already
// reports, so the mapping is the identity and the setup step disappears.
func Identity() *Mapper { return &Mapper{} }

// ToLocal translates a remote path.
func (m *Mapper) ToLocal(remote string) (string, error) {
	if remote == "" {
		return "", &ErrUnmapped{Path: remote}
	}
	if len(m.rules) == 0 {
		return path.Clean(remote), nil
	}
	clean := path.Clean(remote)
	for _, r := range m.rules {
		rr := strings.TrimRight(path.Clean(r.Remote), "/")
		if clean == rr {
			return path.Clean(r.Local), nil
		}
		if strings.HasPrefix(clean, rr+"/") {
			rest := strings.TrimPrefix(clean, rr+"/")
			return path.Join(r.Local, rest), nil
		}
	}
	return "", &ErrUnmapped{Path: remote}
}

// Rules returns the effective, sorted rules — useful for showing the user
// which one matched.
func (m *Mapper) Rules() []Rule { return m.rules }
