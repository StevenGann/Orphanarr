// Package client is the download-client seam.
//
// BRIEF §5 A4 answered "instances AND products": qBittorrent ships first,
// but heterogeneous clients are expected later. That does not justify
// building a second adapter now — an abstraction extracted from one example
// is shaped like that example — but it does justify the four shapes below,
// each of which would cost a schema migration or an API break to add later.
package client

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnsupported is returned by an optional capability the adapter does not
// have. A marker write failing must never fail a plan or trigger a rollback.
var ErrUnsupported = errors.New("client: operation not supported by this client")

// ExternalID is opaque. qBittorrent's happens to be an infohash; SABnzbd and
// NZBGet are Usenet and have none at all. Nothing outside an adapter may
// assume its shape.
type ExternalID string

// Capabilities are PROBED at runtime, per instance — never declared per
// product. A static "deluge has categories" is right on a configured
// instance and catastrophic on a stock one, where the Label plugin is
// absent and every torrent reports as uncategorised.
type Capabilities struct {
	Categories bool `json:"categories"`
	Tags       bool `json:"tags"`
	FileList   bool `json:"file_list"`
}

// Info describes a connected instance.
type Info struct {
	AppVersion string
	APIVersion string
	AuthMode   string
	Caps       Capabilities
}

// Item is one download, normalised. Client-specific state strings never
// escape the adapter.
type Item struct {
	ID   ExternalID
	Name string

	// Category is nil when the client CANNOT EXPRESS categories, which is
	// not the same as an empty category.
	//
	// The pointer is the invariant. Go's zero value for a pointer is nil,
	// so an adapter that forgets to set this files NOTHING rather than
	// files everything — the default wrong answer is the safe one (I14).
	Category *string

	// Complete folds the client's own completeness rules into one answer.
	// For qBittorrent that means state allowlist, progress over WANTED
	// bytes, per-file priority and .!qB markers — all of which are
	// qBittorrent internals and belong here, not in scan/.
	Complete bool

	SavePath    string
	SizeBytes   int64
	Tags        []string
	Infohash    string
	InfohashV2  string
	CompletedOn int64
	State       string
}

// File is one entry from an item's manifest.
//
// The manifest is authoritative and is the ONLY source of work units: I3
// forbids treating content_path as one, because for a multi-file torrent
// with no common root qBittorrent returns the entire shared save path, and
// operating on that touches every other download in the directory.
type File struct {
	// RelPath is relative to the item's save path, forward slashes.
	RelPath string
	Size    int64
	// Wanted is false for deselected files, which qBittorrent parks under
	// .unwanted/ and which must never be filed.
	Wanted bool
}

// DownloadClient is the seam. Note what is ABSENT: no Pause, Delete,
// Recheck, SetLocation, SetCategory. Each has a documented destructive side
// effect and I8 forbids them, so they are not in the interface at all —
// an interface that offers a foot-gun invites its use.
type DownloadClient interface {
	ID() string
	Name() string

	Probe(ctx context.Context) (Info, error)

	// ListItems returns ALL items, not a server-filtered subset. The
	// cross-seed overlap gate needs categorised torrents too, and an
	// unrecognised filter value makes qBittorrent return everything
	// anyway — so a server-side filter cannot even be trusted to have
	// been honoured.
	ListItems(ctx context.Context) ([]Item, error)

	ListFiles(ctx context.Context, id ExternalID) ([]File, error)

	// MarkFiled is the ONLY mutation. It may return ErrUnsupported.
	MarkFiled(ctx context.Context, id ExternalID, marker string) error
}

// Config is what the store holds for one instance.
type Config struct {
	ID       int64
	Name     string
	Kind     string
	BaseURL  string
	Username string
	Password string
	APIKey   string
}

// New builds an adapter. This is the factory the design specifies instead
// of a plugin registry: a switch on a column that already exists, and a
// two-line diff when a second product arrives.
func New(cfg Config) (DownloadClient, error) {
	switch cfg.Kind {
	case "", "qbittorrent":
		return newQBittorrent(cfg)
	default:
		return nil, fmt.Errorf("client: unknown kind %q", cfg.Kind)
	}
}

// CanScan reports whether an instance may be scanned at all.
//
// I14: a client that cannot express "this item has no category" is REFUSED
// at configure time, never scanned. The failure it prevents is total — on a
// stock Deluge every torrent reads as uncategorised, so the orphan predicate
// selects the user's entire seeding library, and under copy-only that is a
// second copy of all of it.
func CanScan(c Capabilities) error {
	if !c.Categories {
		return errors.New(
			"this client cannot express \"no category\", so every item would look like an orphan; " +
				"refusing to scan it (I14)")
	}
	return nil
}
