// Package scan decides which downloads are orphans.
//
// The predicate is conservative by construction: every rule can only
// REMOVE a candidate, never add one, and anything unrecognised fails closed
// into review (I11). The cost of a false negative is that a file waits; the
// cost of a false positive is that Orphanarr copies something it should not
// have touched — and under copy-only that is a full second copy.
package scan

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/StevenGann/Orphanarr/internal/client"
	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/pathmap"
)

// Decision is why a candidate was kept or dropped. The code is from the
// stable event vocabulary so the dashboard can answer "why isn't it doing
// anything" by grouping on it.
type Decision struct {
	Keep   bool
	Code   string
	Detail string
}

func keep() Decision { return Decision{Keep: true} }
func drop(code, detail string) Decision {
	return Decision{Code: code, Detail: detail}
}

// Exclusions are the user's opt-outs.
type Exclusions struct {
	Tags         []string
	SavePaths    []string // glob prefixes
	Trackers     []string
	Fingerprints map[string]bool // sticky "ignore" decisions
}

// Settings are the knobs the predicate reads.
type Settings struct {
	SettleWindow time.Duration
	// LibraryRoots are the configured destinations. A torrent seeding from
	// inside one of them is refused (I5).
	LibraryRoots []string
	Exclusions   Exclusions
}

// Candidate is an item plus everything resolved about it.
type Candidate struct {
	Client client.DownloadClient
	Item   client.Item
	Files  []client.File
	// LocalPaths are the container-visible absolute paths of wanted files.
	LocalPaths  []string
	Fingerprint string
	FirstSeen   time.Time
}

// Evaluate applies O1–O11 in order. Ordering is deliberate: the cheap,
// certain rules run before the ones that need I/O.
func Evaluate(c Candidate, s Settings, now time.Time) Decision {
	it := c.Item

	// O1 — the orphan test itself. A nil Category means the client cannot
	// express categories at all, which is not "no category": it is "this
	// client must not be scanned" (I14), and reaching here with nil means
	// a configure-time check was skipped.
	if it.Category == nil {
		return drop("SKIP_NO_CATEGORY_SUPPORT",
			"client cannot express categories; it should have been refused at configure time (I14)")
	}
	// Whitespace is NOT empty. qBittorrent's isValidCategoryName() accepts
	// a single space — [^\\/] matches it — and createCategoryAction does
	// not trim before validating. So " " is a real category a user can
	// create, and TrimSpace here treated it as uncategorised and filed the
	// torrent. The comment said one thing and the code did the other; the
	// comment was right.
	if *it.Category != "" {
		return drop("SKIP_CATEGORIZED", "category is "+strconv.Quote(*it.Category))
	}

	// O2 — complete. The adapter folded the client's own rules into this.
	if !it.Complete {
		return drop("SKIP_INCOMPLETE", "state="+it.State)
	}

	// O7 — the settle window. A torrent that completed thirty seconds ago
	// may still be moving bytes, and completion_on is -1 on migrated
	// clients, so first_seen_at is what the window is measured from.
	if age := now.Sub(c.FirstSeen); age < s.SettleWindow {
		return drop("SETTLE_PENDING",
			fmt.Sprintf("seen %s ago, settle window is %s", age.Truncate(time.Second), s.SettleWindow))
	}

	// O11 — user exclusions.
	for _, t := range it.Tags {
		for _, ex := range s.Exclusions.Tags {
			if strings.EqualFold(t, ex) {
				return drop("SKIP_IGNORED", "excluded by tag "+t)
			}
		}
	}
	for _, p := range s.Exclusions.SavePaths {
		// On a path BOUNDARY, not a bare prefix: excluding
		// /data/torrents/tv must not also exclude
		// /data/torrents/tvshows-keep. Over-exclusion fails safe, but a
		// user who cannot tell which of their directories are covered
		// cannot configure this at all.
		if p != "" && underPath(it.SavePath, p) {
			return drop("SKIP_EXCLUDED", "save path excluded by "+p)
		}
	}
	if s.Exclusions.Fingerprints[c.Fingerprint] {
		return drop("SKIP_IGNORED", "user marked this content ignore")
	}

	// O3/O5 — the manifest must exist and contain wanted files. An empty
	// manifest is not an empty payload; it is a client we could not read.
	if len(c.Files) == 0 {
		return drop("SKIP_NO_METADATA", "empty file manifest")
	}
	wanted := 0
	for _, f := range c.Files {
		if f.Wanted {
			wanted++
		}
	}
	if wanted == 0 {
		return drop("SKIP_PARTIAL_SELECTION", "every file is deselected")
	}

	// O6 — qBittorrent's incomplete marker. A .!qB file means bytes are
	// still arriving whatever the state says.
	for _, f := range c.Files {
		if strings.HasSuffix(f.RelPath, ".!qB") {
			return drop("SKIP_QB_MARKER", "incomplete marker present: "+f.RelPath)
		}
	}

	// I5/O9 — never process a torrent any of whose resolved paths lies
	// inside a configured library root.
	//
	// The exposed population is anyone who previously imported by hardlink
	// and now seeds from their library: filing it copies the payload back
	// into the same library under a new name, and under copy-only that is
	// a full duplicate of something already there.
	for _, lp := range c.LocalPaths {
		for _, root := range s.LibraryRoots {
			if root != "" && underPath(lp, root) {
				return drop("SEEDING_FROM_LIBRARY",
					"a payload path is inside the library root "+root)
			}
		}
	}

	// O8 — every wanted file must resolve to a path this container can
	// see. Unmapped means refuse, never guess (BRIEF §5 A3 keeps every
	// client local, so an unmapped path is a misconfiguration).
	if len(c.LocalPaths) == 0 {
		return drop("SKIP_UNMAPPED", "no wanted file resolved to a local path")
	}

	return keep()
}

// underPath reports whether p is root or lies beneath it, comparing whole
// path components so that a shared prefix is not mistaken for containment.
func underPath(p, root string) bool {
	p, root = path.Clean(p), path.Clean(strings.TrimRight(root, "/"))
	return p == root || strings.HasPrefix(p, root+"/")
}

// Fingerprint identifies content independently of path or infohash.
//
// It is the cross-client identity key: computed from the manifest, so it
// works for SABnzbd where there is no infohash at all, and it is what
// sticky user decisions and the third leg of the overlap gate hang off.
func Fingerprint(files []client.File) string {
	type e struct {
		name string
		size int64
	}
	var list []e
	for _, f := range files {
		if !f.Wanted {
			continue
		}
		list = append(list, e{path.Base(f.RelPath), f.Size})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].size != list[j].size {
			return list[i].size < list[j].size
		}
		return list[i].name < list[j].name
	})

	var b strings.Builder
	for _, x := range list {
		fmt.Fprintf(&b, "%s:%d;", x.name, x.size)
	}
	return hashString(b.String())
}

// Overlap is the cross-seed gate (O10, I4).
//
// Three legs, unioned. Under BRIEF §5 A3 the container mounts the clients'
// download folders — PLURAL — so two clients sharing one host directory
// produce ONE physical file and TWO path strings. A path-equality test
// alone finds no overlap, the collapse never fires, and the highest-damage
// false-positive class fires cleanly. Under copy-only that costs a full
// duplicate copy.
type Overlap struct {
	byPath        map[string][]int
	byInode       map[[2]uint64][]int
	byFingerprint map[string][]int

	// keysOf lets Peers find an index's own buckets directly. Scanning
	// every inode bucket per call was O(candidates x indexed files) — about
	// 8e9 iterations on a 40,000-torrent library.
	pathsOf  map[int][]string
	inodesOf map[int][][2]uint64
	fpOf     map[int]string
}

func NewOverlap() *Overlap {
	return &Overlap{
		byPath:        map[string][]int{},
		byInode:       map[[2]uint64][]int{},
		byFingerprint: map[string][]int{},
		pathsOf:       map[int][]string{},
		inodesOf:      map[int][][2]uint64{},
		fpOf:          map[int]string{},
	}
}

// Add indexes one candidate. fs may be nil, in which case the inode leg is
// skipped and only path and fingerprint are indexed.
func (o *Overlap) Add(idx int, c Candidate, fs fsx.FS) {
	for _, p := range c.LocalPaths {
		clean := path.Clean(p)
		o.byPath[clean] = append(o.byPath[clean], idx)
		o.pathsOf[idx] = append(o.pathsOf[idx], clean)

		if fs != nil {
			if fi, err := fs.Stat(clean); err == nil {
				key := [2]uint64{fi.Dev, fi.Ino}
				o.byInode[key] = append(o.byInode[key], idx)
				o.inodesOf[idx] = append(o.inodesOf[idx], key)
			}
		}
	}
	if c.Fingerprint != "" {
		o.byFingerprint[c.Fingerprint] = append(o.byFingerprint[c.Fingerprint], idx)
		o.fpOf[idx] = c.Fingerprint
	}
}

// AddPeers indexes candidates that are NOT themselves eligible — the
// categorised torrents — and returns the set of candidate indexes that
// overlap one.
//
// This is O10's first clause and §3.4 FP-1: one physical payload, two
// torrents, one of them owned by an *arr. Filing the uncategorised half
// produces a duplicate library entry and, under copy-only, a full second
// copy of content already imported.
func (o *Overlap) AddPeers(peers []Candidate, fs fsx.FS) map[int]bool {
	blocked := map[int]bool{}
	mark := func(list []int) {
		for _, i := range list {
			blocked[i] = true
		}
	}
	for _, pc := range peers {
		for _, p := range pc.LocalPaths {
			clean := path.Clean(p)
			mark(o.byPath[clean])
			if fs != nil {
				if fi, err := fs.Stat(clean); err == nil {
					mark(o.byInode[[2]uint64{fi.Dev, fi.Ino}])
				}
			}
		}
		if pc.Fingerprint != "" {
			mark(o.byFingerprint[pc.Fingerprint])
		}
	}
	return blocked
}

// Transitive returns idx plus every candidate reachable from it through
// any leg, following chains.
//
// Without the closure, A~B by fingerprint and B~C by path leaves C planned
// separately for the same payload: the loop claims B and moves on before
// ever asking what B overlaps.
func (o *Overlap) Transitive(idx int, all []Candidate) []int {
	seen := map[int]bool{idx: true}
	queue := []int{idx}
	var out []int

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur >= len(all) {
			continue
		}
		for _, peer := range o.Peers(cur, all[cur]) {
			if seen[peer] {
				continue
			}
			seen[peer] = true
			out = append(out, peer)
			queue = append(queue, peer)
		}
	}
	sort.Ints(out)
	return out
}

// Peers returns every other candidate index that shares a path, an inode or
// a fingerprint with idx.
//
// The fingerprint leg exists because the inode leg narrows the hole rather
// than closing it: every alias crossing a FUSE boundary — unRAID
// /mnt/user vs /mnt/disk1, mergerfs pool vs branch — has st_dev differing
// BY CONSTRUCTION, so the path and inode legs fail together. Both are named
// target platforms.
func (o *Overlap) Peers(idx int, c Candidate) []int {
	seen := map[int]bool{}
	add := func(list []int) {
		for _, i := range list {
			if i != idx {
				seen[i] = true
			}
		}
	}
	for _, p := range o.pathsOf[idx] {
		add(o.byPath[p])
	}
	for _, key := range o.inodesOf[idx] {
		add(o.byInode[key])
	}
	if fp := o.fpOf[idx]; fp != "" {
		add(o.byFingerprint[fp])
	}

	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// ErrEscapes means a manifest entry resolved outside its own save path.
var ErrEscapes = errors.New("scan: a manifest path escapes the save path")

// ResolveLocal maps a candidate's wanted files into container paths.
//
// Every entry is checked for containment. A manifest is client-supplied
// data derived from a .torrent file, so a name like "../../etc/passwd" is
// hostile input arriving through a trusted-looking channel — and
// path.Join collapses it silently, yielding a real path outside every
// configured root. Whether libtorrent can actually emit one is
// [UNVERIFIED]; the check costs one comparison and the consequence of
// being wrong is a read, a stat, and a probe-file create somewhere nobody
// configured.
func ResolveLocal(m *pathmap.Mapper, item client.Item, files []client.File) ([]string, error) {
	base, err := m.ToLocal(item.SavePath)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if !f.Wanted {
			continue
		}
		p := path.Join(base, f.RelPath)
		if !underPath(p, base) {
			return nil, fmt.Errorf("%w: %q resolved to %q, outside %q",
				ErrEscapes, f.RelPath, p, base)
		}
		out = append(out, p)
	}
	return out, nil
}
