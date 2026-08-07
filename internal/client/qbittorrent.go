package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// qbClient talks to the qBittorrent WebUI API v2.
//
// Several things here contradict the published wiki. Each is sourced in
// DESIGN Appendix A and verified by tests/verification/qbittorrent_contract_test.py.
type qbClient struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	loggedIn bool
	caps     Capabilities
}

func newQBittorrent(cfg Config) (DownloadClient, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("qbittorrent: base URL is required")
	}
	jar := &cookieJar{}
	return &qbClient{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
			// Go's http.Client re-adds Referer on redirect-followed
			// requests, which defeats the "send neither header" rule
			// below and produces exactly the rejection it avoids.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				req.Header.Del("Referer")
				req.Header.Del("Origin")
				if len(via) >= 5 {
					return errors.New("qbittorrent: too many redirects")
				}
				return nil
			},
		},
	}, nil
}

func (q *qbClient) ID() string   { return strconv.FormatInt(q.cfg.ID, 10) }
func (q *qbClient) Name() string { return q.cfg.Name }

func (q *qbClient) endpoint(p string) string {
	return strings.TrimRight(q.cfg.BaseURL, "/") + "/api/v2/" + strings.TrimLeft(p, "/")
}

// do issues a request.
//
// It deliberately sends NEITHER Referer NOR Origin. The usual advice is
// backwards: qBittorrent's isCrossSiteRequest() returns "not cross-site"
// when both are absent — the source comment is literally "lets be
// permissive here" — whereas injecting Referer moves the request into the
// branch that must match Host, which any reverse proxy rewrites.
func (q *qbClient) do(ctx context.Context, method, path string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, q.endpoint(path), body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// API-key auth (qBittorrent >= 5.2) skips the cookie dance entirely.
	if q.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+q.cfg.APIKey)
	}
	return q.http.Do(req)
}

// login authenticates and stores the SID cookie.
//
// Success is 200 with an EMPTY body on 5.x, not the body "Ok." the wiki
// documents, so authentication is judged on status code plus the presence
// of the SID cookie. Failure is 401, not 403.
func (q *qbClient) login(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.loggedIn || q.cfg.APIKey != "" {
		return nil
	}
	if q.cfg.Username == "" {
		// Local-network bypass or an open WebUI: try unauthenticated.
		q.loggedIn = true
		return nil
	}

	form := url.Values{"username": {q.cfg.Username}, "password": {q.cfg.Password}}
	resp, err := q.do(ctx, http.MethodPost, "auth/login", form)
	if err != nil {
		return fmt.Errorf("qbittorrent: login: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		q.loggedIn = true
		return nil
	case http.StatusForbidden:
		// 403 on auth/login is the WebUIMaxAuthFailCount IP BAN, thrown
		// only from validateCredentials() — a different condition from a
		// wrong password, and one that retrying makes worse.
		return errors.New("qbittorrent: IP banned by WebUIMaxAuthFailCount; " +
			"wait for the ban to expire or restart qBittorrent. Do not retry credentials")
	case http.StatusUnauthorized:
		return errors.New("qbittorrent: authentication failed (401): bad username or password")
	default:
		return fmt.Errorf("qbittorrent: login returned %s", resp.Status)
	}
}

func (q *qbClient) getJSON(ctx context.Context, path string, out any) error {
	if err := q.login(ctx); err != nil {
		return err
	}
	resp, err := q.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		// 403 on a NON-login endpoint means the session expired. Re-login
		// once and retry once; a second 403 is a real auth failure.
		q.mu.Lock()
		q.loggedIn = false
		q.mu.Unlock()
		if err := q.login(ctx); err != nil {
			return err
		}
		resp2, err := q.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			return fmt.Errorf("qbittorrent: %s returned %s after relogin", path, resp2.Status)
		}
		return json.NewDecoder(resp2.Body).Decode(out)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent: %s returned %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (q *qbClient) getText(ctx context.Context, path string) (string, error) {
	if err := q.login(ctx); err != nil {
		return "", err
	}
	resp, err := q.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qbittorrent: %s returned %s", path, resp.Status)
	}
	return strings.TrimSpace(string(b)), nil
}

func (q *qbClient) Probe(ctx context.Context) (Info, error) {
	app, err := q.getText(ctx, "app/version")
	if err != nil {
		return Info{}, err
	}
	api, err := q.getText(ctx, "app/webapiVersion")
	if err != nil {
		return Info{}, err
	}

	// Probe capabilities rather than assuming them. qBittorrent has always
	// had categories and tags, but asking costs one request and makes the
	// per-instance rule real rather than aspirational.
	caps := Capabilities{FileList: true}
	var cats map[string]any
	if err := q.getJSON(ctx, "torrents/categories", &cats); err == nil {
		caps.Categories = true
	}
	var tags []string
	if err := q.getJSON(ctx, "torrents/tags", &tags); err == nil {
		caps.Tags = true
	}

	mode := "cookie"
	if q.cfg.APIKey != "" {
		mode = "api-key"
	}

	q.mu.Lock()
	q.caps = caps
	q.mu.Unlock()

	return Info{AppVersion: app, APIVersion: api, AuthMode: mode, Caps: caps}, nil
}

// wireTorrent mirrors serialize_torrent.h. Only the fields the predicate
// actually branches on are named.
type wireTorrent struct {
	Hash         string  `json:"hash"`
	InfohashV1   string  `json:"infohash_v1"`
	InfohashV2   string  `json:"infohash_v2"`
	Name         string  `json:"name"`
	Category     string  `json:"category"`
	Tags         string  `json:"tags"`
	SavePath     string  `json:"save_path"`
	ContentPath  string  `json:"content_path"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
	TotalSize    int64   `json:"total_size"`
	AmountLeft   int64   `json:"amount_left"`
	CompletionOn int64   `json:"completion_on"`
}

// completeStates is an ALLOWLIST. Unknown states fail closed (I11).
//
// The 5.x rename is real: the source emits stoppedUP/stoppedDL where the
// published 5.0 wiki still documents pausedUP/pausedDL. Both spellings are
// accepted because both are shipped by supported versions.
var completeStates = map[string]bool{
	"uploading": true,
	"stalledUP": true,
	"queuedUP":  true,
	"forcedUP":  true,
	"pausedUP":  true, // <= 4.x
	"stoppedUP": true, // >= 5.0
}

// movingOrCheckingStates mean bytes are in motion or the path is about to
// change. progress == 1 is NOT sufficient on its own.
var movingOrCheckingStates = map[string]bool{
	"checkingUP": true, "checkingDL": true, "checkingResumeData": true,
	"moving": true, "allocating": true, "metaDL": true, "forcedMetaDL": true,
}

func (q *qbClient) ListItems(ctx context.Context) ([]Item, error) {
	var wire []wireTorrent
	// filter=all, filtered locally. A server-side category filter is
	// incompatible with the cross-seed overlap gate, which needs
	// categorised torrents too — and an unrecognised filter value makes
	// qBittorrent return everything anyway (parseTorrentStatus() ends
	// `return TorrentFilter::All`), so the filter cannot be trusted to
	// have been honoured.
	if err := q.getJSON(ctx, "torrents/info?filter=all", &wire); err != nil {
		return nil, err
	}

	q.mu.Lock()
	caps := q.caps
	q.mu.Unlock()

	out := make([]Item, 0, len(wire))
	for _, w := range wire {
		id := w.Hash
		if id == "" {
			id = w.InfohashV1
		}

		it := Item{
			ID:          ExternalID(id),
			Name:        w.Name,
			SavePath:    w.SavePath,
			SizeBytes:   w.Size,
			Infohash:    w.InfohashV1,
			InfohashV2:  w.InfohashV2,
			CompletedOn: w.CompletionOn,
			State:       w.State,
			Tags:        splitTags(w.Tags),
			Complete:    isComplete(w),
		}
		// Only set Category when the instance can actually express one.
		// Leaving it nil otherwise is what makes I14 mechanical.
		if caps.Categories {
			cat := w.Category
			it.Category = &cat
		}
		out = append(out, it)
	}
	return out, nil
}

// isComplete folds every qBittorrent-specific completeness rule into one
// boolean, so scan/ keeps only Orphanarr policy.
func isComplete(w wireTorrent) bool {
	if movingOrCheckingStates[w.State] {
		return false
	}
	if !completeStates[w.State] {
		return false
	}
	// progress is over WANTED bytes, so a partially-selected torrent
	// reports 1.0 while most of the payload is absent. amount_left == 0 is
	// the corroborating check.
	if w.Progress < 1 || w.AmountLeft != 0 {
		return false
	}
	// completion_on is -1 when qBittorrent never observed completion — a
	// migrated client, which is one of the orphan populations the brief
	// names. That is not a reason to skip, only a reason not to trust the
	// timestamp for the settle window.
	return true
}

// splitTags splits qBittorrent's tag string.
//
// Tags are joined with ", " — comma AND SPACE. A naive split(",") leaves a
// leading space on every tag after the first, so an exclusion tag like
// "orphanarr-ignore" silently never matches and the opt-out does nothing.
func splitTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

type wireFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Priority int    `json:"priority"`
}

func (q *qbClient) ListFiles(ctx context.Context, id ExternalID) ([]File, error) {
	var wire []wireFile
	if err := q.getJSON(ctx, "torrents/files?hash="+url.QueryEscape(string(id)), &wire); err != nil {
		return nil, err
	}
	out := make([]File, 0, len(wire))
	for _, w := range wire {
		out = append(out, File{
			RelPath: w.Name,
			Size:    w.Size,
			// Priority 0 is "do not download". Those files are parked
			// under .unwanted/ and are precisely the population the
			// predicate exists to exclude.
			Wanted: w.Priority != 0,
		})
	}
	return out, nil
}

// MarkFiled adds a tag. This is the ONLY write.
//
// Deliberately NOT setCategory: under Automatic Torrent Management a
// category carries a save path, so setting one physically RELOCATES the
// user's data. That is a destructive side effect hiding behind what looks
// like bookkeeping.
func (q *qbClient) MarkFiled(ctx context.Context, id ExternalID, marker string) error {
	q.mu.Lock()
	caps := q.caps
	q.mu.Unlock()
	if !caps.Tags {
		return ErrUnsupported
	}
	if err := q.login(ctx); err != nil {
		return err
	}

	form := url.Values{"hashes": {string(id)}, "tags": {marker}}
	resp, err := q.do(ctx, http.MethodPost, "torrents/addTags", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent: addTags returned %s", resp.Status)
	}
	return nil
}

// cookieJar is a minimal per-client jar. net/http/cookiejar pulls in the
// public-suffix list for a single host we already trust.
type cookieJar struct {
	mu      sync.Mutex
	cookies []*http.Cookie
}

func (j *cookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = append(j.cookies, cookies...)
}

func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cookies
}
