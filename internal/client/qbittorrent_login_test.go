package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The auth/login contract is split by version and every arm of it has
// burned us or nearly did. Pin all four.
//
//	<= 5.1.x   success -> 200 "Ok."      failure -> 200 "Fails."
//	>= 5.2.0   success -> 204 (empty)    failure -> 401
//
// The 204 arm is the regression this file was written for: a real
// qBittorrent 5.2.1 answered "HTTP/1.1 204 OK" and the default: arm of the
// status switch turned every success into "login returned 204 OK".
func TestLoginContractByVersion(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		reason  string
		body    string
		wantErr string // substring; empty means login must succeed
	}{
		{name: "5.1 success", status: 200, body: "Ok."},
		{name: "5.1 failure", status: 200, body: "Fails.", wantErr: "bad username or password"},
		{name: "5.2 success is 204", status: 204},
		{name: "5.2 failure", status: 401, wantErr: "401"},
		{name: "ban is 403", status: 403, wantErr: "IP banned"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/auth/login" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			q, err := newQBittorrent(Config{
				BaseURL: srv.URL, Username: "admin", Password: "hunter2",
			})
			if err != nil {
				t.Fatalf("newQBittorrent: %v", err)
			}

			err = q.(*qbClient).login(context.Background())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("login: want success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("login: want error containing %q, got success", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("login: want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// A 204 must leave the client usable, not merely un-errored: the bug made
// Probe fail, so the assertion that matters is an end-to-end call.
func TestProbeSucceedsAgainst52StyleServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v5.2.1"))
		case "/api/v2/app/webapiVersion":
			_, _ = w.Write([]byte("2.11.4"))
		case "/api/v2/torrents/categories":
			_, _ = w.Write([]byte(`{"books":{"name":"books","savePath":""}}`))
		case "/api/v2/torrents/tags":
			_, _ = w.Write([]byte(`["seeding"]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	q, err := newQBittorrent(Config{BaseURL: srv.URL, Username: "admin", Password: "hunter2"})
	if err != nil {
		t.Fatalf("newQBittorrent: %v", err)
	}
	info, err := q.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.AppVersion != "v5.2.1" {
		t.Errorf("AppVersion = %q, want v5.2.1", info.AppVersion)
	}
	// I14: a client that cannot express categories may not be scanned, so
	// the probe reporting them is what makes this instance usable at all.
	if !info.Caps.Categories {
		t.Error("Caps.Categories = false, want true")
	}
}
