// Package api serves the JSON API and the embedded UI.
//
// The API is the seam. DESIGN D2 records that the SPA-versus-server-rendered
// choice was close and explicitly notes "if this flips, nothing else in the
// design changes" — which is what makes shipping a build-step-free UI here a
// deviation the design already sanctions rather than a departure from it.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/store"
	"github.com/StevenGann/Orphanarr/internal/web"
)

// Scanner is what the API can trigger. Kept as an interface so the API can
// be tested without a download client.
type Scanner interface {
	ScanNow(ctx context.Context) (Summary, error)
	Status(ctx context.Context) Status
}

// Summary reports one scan.
type Summary struct {
	Items      int            `json:"items"`
	Orphans    int            `json:"orphans"`
	Skipped    map[string]int `json:"skipped"`
	Classified map[string]int `json:"classified"`
	Bytes      int64          `json:"bytes"`
	Errors     []string       `json:"errors,omitempty"`
}

// Status is what a monitoring check should watch.
type Status struct {
	Version     string            `json:"version"`
	DryRun      bool              `json:"dry_run"`
	Clients     []ClientStatus    `json:"clients"`
	Libraries   []LibraryStatus   `json:"libraries"`
	SkipReasons map[string]int    `json:"skip_reasons"`
	Problems    []string          `json:"problems,omitempty"`
	Settings    map[string]string `json:"settings"`
}

type ClientStatus struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Reachable  bool   `json:"reachable"`
	AppVersion string `json:"app_version,omitempty"`
	Error      string `json:"error,omitempty"`
	Scannable  bool   `json:"scannable"`
	Refusal    string `json:"refusal,omitempty"`
}

type LibraryStatus struct {
	Type       string `json:"type"`
	Root       string `json:"root"`
	Enabled    bool   `json:"enabled"`
	Writable   bool   `json:"writable"`
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	// Hardlinks carries the three-way probe result, not a boolean:
	// "copy only — source is on a read-only mount" is a distinct outcome
	// from "copy only — separate mounts", and without it the remediation
	// banner tells a user to do what they have already done.
	Hardlinks string `json:"hardlinks"`
}

// Server wires the routes.
type Server struct {
	db      *store.DB
	cfg     config.Config
	scanner Scanner
	version string
}

func New(db *store.DB, cfg config.Config, sc Scanner, version string) *Server {
	return &Server{db: db, cfg: cfg, scanner: sc, version: version}
}

// NewAPIKey generates a key. Shown once, at first run.
func NewAPIKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Liveness is deliberately unauthenticated so HEALTHCHECK works
	// without baking a key into the image.
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version})
	})

	mux.Handle("GET /api/v1/system/status", s.auth(http.HandlerFunc(s.getStatus)))
	mux.Handle("GET /api/v1/settings", s.auth(http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT /api/v1/settings", s.auth(http.HandlerFunc(s.putSettings)))
	mux.Handle("GET /api/v1/events", s.auth(http.HandlerFunc(s.getEvents)))
	mux.Handle("POST /api/v1/scan", s.auth(http.HandlerFunc(s.postScan)))

	mux.Handle("/", web.Handler())
	return logging(mux)
}

// auth gates everything except health.
//
// The UI passes the key too, so there is exactly one authentication path
// rather than a session mechanism beside an API key — two paths means two
// places to get session hardening wrong, and BRIEF Q19 flags that this
// program is remote arbitrary file write by design.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" {
			// No key configured yet: first run, before the wizard has set
			// one. Allow, because otherwise the user cannot reach the UI
			// that sets it.
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = r.URL.Query().Get("apikey")
		}
		// Constant time: a timing oracle on an API key is a real leak even
		// on a LAN.
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.APIKey)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing X-Api-Key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	st := s.scanner.Status(r.Context())
	st.Version = s.version
	st.DryRun = s.cfg.DryRun
	if counts, err := s.db.SkipReasonCounts(r.Context()); err == nil {
		st.SkipReasons = counts
	}
	st.Problems = append(st.Problems, s.cfg.Validate()...)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.db.AllSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":  all,
		"read_only": s.cfg.ReadOnly,
		"defaults":  config.Defaults(),
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for k, v := range body {
		if s.cfg.ReadOnly[k] {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": k + " is pinned by an environment variable and cannot be edited here",
			})
			return
		}
		if err := s.db.SetSetting(r.Context(), k, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.db.RecentEvents(r.Context(), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) postScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	sum, err := s.scanner.ScanNow(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(), "partial": sum,
		})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never log the query string: the API key can travel in it for the
		// UI's benefit, and a logged key is a leaked key.
		path := r.URL.Path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		next.ServeHTTP(w, r)
	})
}
