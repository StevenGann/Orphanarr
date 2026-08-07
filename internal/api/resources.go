package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// Manager is what the API can ask the pipeline to do beyond scanning.
// Kept narrow and interface-shaped so the HTTP layer can be exercised
// without a download client or a filesystem.
type Manager interface {
	Reload(ctx context.Context) error
	ApplyConfig(ctx context.Context) (config.Config, error)
	Execute(ctx context.Context, planID int64) error
	Undo(ctx context.Context, planID int64) error
	TestClient(ctx context.Context, c store.Client) (map[string]any, error)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/clients", s.auth(http.HandlerFunc(s.listClients)))
	mux.Handle("POST /api/v1/clients", s.auth(http.HandlerFunc(s.saveClient)))
	mux.Handle("PUT /api/v1/clients/{id}", s.auth(http.HandlerFunc(s.saveClient)))
	mux.Handle("DELETE /api/v1/clients/{id}", s.auth(http.HandlerFunc(s.deleteClient)))
	mux.Handle("POST /api/v1/clients/test", s.auth(http.HandlerFunc(s.testClient)))

	mux.Handle("GET /api/v1/libraries", s.auth(http.HandlerFunc(s.listLibraries)))
	mux.Handle("POST /api/v1/libraries", s.auth(http.HandlerFunc(s.saveLibrary)))
	mux.Handle("DELETE /api/v1/libraries/{id}", s.auth(http.HandlerFunc(s.deleteLibrary)))

	mux.Handle("GET /api/v1/orphans", s.auth(http.HandlerFunc(s.listOrphans)))
	mux.Handle("GET /api/v1/orphans/{id}/explain", s.auth(http.HandlerFunc(s.explainOrphan)))
	mux.Handle("POST /api/v1/orphans/{id}/ignore", s.auth(http.HandlerFunc(s.ignoreOrphan)))

	mux.Handle("GET /api/v1/plans", s.auth(http.HandlerFunc(s.listPlans)))
	mux.Handle("GET /api/v1/plans/{id}", s.auth(http.HandlerFunc(s.getPlan)))
	mux.Handle("POST /api/v1/plans/{id}/execute", s.auth(http.HandlerFunc(s.executePlan)))
	mux.Handle("POST /api/v1/plans/{id}/undo", s.auth(http.HandlerFunc(s.undoPlan)))
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// ------------------------------------------------------------------ clients

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListClients(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// Credentials are never returned. HasSecret lets the UI show
	// "unchanged" rather than an empty box that reads as "not set".
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"client":     c.Redacted(),
			"has_secret": c.HasSecret(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

func (s *Server) saveClient(w http.ResponseWriter, r *http.Request) {
	var c store.Client
	if err := decode(w, r, &c); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if id, err := pathID(r); err == nil {
		c.ID = id
	}
	if c.Name == "" || c.BaseURL == "" {
		fail(w, http.StatusBadRequest, errors.New("name and base_url are required"))
		return
	}

	id, err := s.db.SaveClient(r.Context(), c)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// Reload re-probes and re-registers roots with the filesystem guard.
	// Without it the guard would still be protecting the previous set of
	// download roots, which is the one thing that must never lag config.
	if s.mgr != nil {
		if err := s.mgr.Reload(r.Context()); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "warning": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.DeleteClient(r.Context(), id); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.mgr != nil {
		s.mgr.Reload(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// testClient probes WITHOUT saving, so a user finds out the credentials are
// wrong before committing them — and finds out whether the instance can
// express categories at all, which decides whether it may be scanned (I14).
func (s *Server) testClient(w http.ResponseWriter, r *http.Request) {
	var c store.Client
	if err := decode(w, r, &c); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.mgr == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("no manager wired"))
		return
	}
	// An existing client posted without a password means "use the stored
	// one": the UI never received it and so cannot send it back.
	if c.ID != 0 && c.Password == "" && c.APIKey == "" {
		if stored, err := s.db.GetClient(r.Context(), c.ID); err == nil {
			c.Password, c.APIKey = stored.Password, stored.APIKey
		}
	}

	res, err := s.mgr.TestClient(r.Context(), c)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------- libraries

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListLibraries(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": list})
}

func (s *Server) saveLibrary(w http.ResponseWriter, r *http.Request) {
	var l store.Library
	if err := decode(w, r, &l); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.SaveLibrary(r.Context(), l); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.mgr != nil {
		s.mgr.Reload(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.DeleteLibrary(r.Context(), id); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if s.mgr != nil {
		s.mgr.Reload(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ------------------------------------------------------------------ orphans

func (s *Server) listOrphans(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListOrphans(r.Context(), r.URL.Query().Get("state"), 300)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orphans": list})
}

// explainOrphan returns the full signal trail.
//
// This is a debugging endpoint AND a user-facing feature: it is what makes
// "why did it think that was a comic" answerable by the person affected
// rather than by a maintainer reading logs.
func (s *Server) explainOrphan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	o, err := s.db.GetOrphan(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// ignoreOrphan records a sticky decision keyed on the content fingerprint,
// so it survives the torrent being re-added under a different infohash.
func (s *Server) ignoreOrphan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.db.SetOrphanState(r.Context(), id, "ignored"); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// -------------------------------------------------------------------- plans

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListPlans(r.Context(), r.URL.Query().Get("status"), 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": list})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.db.GetPlan(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// executePlan is the only path from a plan to a file.
//
// It refuses in dry-run with a sentence rather than an errno, and its
// preflight refuses before writing a byte rather than part-way through.
func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.mgr == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("no manager wired"))
		return
	}
	if err := s.mgr.Execute(r.Context(), id); err != nil {
		// 409: the request was well-formed and the server understood it;
		// the state of the world refused it. That distinction matters to a
		// UI deciding whether to offer a retry.
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "executed"})
}

func (s *Server) undoPlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.mgr == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("no manager wired"))
		return
	}
	if err := s.mgr.Undo(r.Context(), id); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "undone"})
}
