package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Client is one configured download client instance.
type Client struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	// Password is write-only over the API: it is accepted on save and
	// never returned, so a read of the config page cannot leak it into a
	// browser cache, a screenshot or a support paste.
	Password string `json:"password,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Enabled  bool   `json:"enabled"`

	Caps      map[string]bool `json:"caps"`
	LastError string          `json:"last_error"`

	Mappings []PathMapping `json:"mappings"`
}

// PathMapping translates one remote prefix to one local prefix.
type PathMapping struct {
	ID     int64  `json:"id"`
	Remote string `json:"remote"`
	Local  string `json:"local"`
}

// Redacted returns a copy safe to serialise to the UI.
func (c Client) Redacted() Client {
	c.Password = ""
	c.APIKey = ""
	return c
}

// HasSecret reports whether a credential is stored, so the UI can show
// "unchanged" rather than an empty box that looks like "not set".
func (c Client) HasSecret() bool { return c.Password != "" || c.APIKey != "" }

func (d *DB) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT id, name, kind, base_url, username, password, api_key, enabled,
               caps_json, last_error
        FROM client ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Client
	for rows.Next() {
		var c Client
		var caps string
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.BaseURL, &c.Username,
			&c.Password, &c.APIKey, &c.Enabled, &caps, &c.LastError); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(caps), &c.Caps)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		m, err := d.listMappings(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Mappings = m
	}
	return out, nil
}

func (d *DB) GetClient(ctx context.Context, id int64) (Client, error) {
	var c Client
	var caps string
	err := d.sql.QueryRowContext(ctx, `
        SELECT id, name, kind, base_url, username, password, api_key, enabled,
               caps_json, last_error
        FROM client WHERE id = ?`, id).Scan(&c.ID, &c.Name, &c.Kind, &c.BaseURL,
		&c.Username, &c.Password, &c.APIKey, &c.Enabled, &caps, &c.LastError)
	if err != nil {
		return c, err
	}
	json.Unmarshal([]byte(caps), &c.Caps)
	c.Mappings, err = d.listMappings(ctx, c.ID)
	return c, err
}

func (d *DB) listMappings(ctx context.Context, clientID int64) ([]PathMapping, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, remote, local FROM path_mapping WHERE client_id = ? ORDER BY length(remote) DESC`,
		clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PathMapping{}
	for rows.Next() {
		var m PathMapping
		if err := rows.Scan(&m.ID, &m.Remote, &m.Local); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveClient inserts or updates. An empty password or API key on update
// means "leave unchanged", because the UI never receives the stored value
// and so cannot echo it back.
func (d *DB) SaveClient(ctx context.Context, c Client) (int64, error) {
	if c.Kind == "" {
		c.Kind = "qbittorrent"
	}
	caps, _ := json.Marshal(c.Caps)

	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if c.ID == 0 {
		res, err := tx.ExecContext(ctx, `
            INSERT INTO client(name, kind, base_url, username, password, api_key,
                               enabled, caps_json, created_at)
            VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Name, c.Kind, c.BaseURL, c.Username, c.Password, c.APIKey,
			c.Enabled, string(caps), now())
		if err != nil {
			return 0, err
		}
		c.ID, _ = res.LastInsertId()
	} else {
		_, err := tx.ExecContext(ctx, `
            UPDATE client SET name=?, kind=?, base_url=?, username=?,
                   enabled=?, caps_json=?
            WHERE id=?`,
			c.Name, c.Kind, c.BaseURL, c.Username, c.Enabled, string(caps), c.ID)
		if err != nil {
			return 0, err
		}
		if c.Password != "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE client SET password=? WHERE id=?`, c.Password, c.ID); err != nil {
				return 0, err
			}
		}
		if c.APIKey != "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE client SET api_key=? WHERE id=?`, c.APIKey, c.ID); err != nil {
				return 0, err
			}
		}
	}

	// Mappings are replaced wholesale. They are a small ordered set the
	// user edits as a unit, and diffing them would buy nothing but a
	// chance to leave a stale row behind.
	if _, err := tx.ExecContext(ctx, `DELETE FROM path_mapping WHERE client_id = ?`, c.ID); err != nil {
		return 0, err
	}
	for _, m := range c.Mappings {
		if m.Remote == "" || m.Local == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO path_mapping(client_id, remote, local) VALUES(?, ?, ?)`,
			c.ID, m.Remote, m.Local); err != nil {
			return 0, err
		}
	}

	return c.ID, tx.Commit()
}

func (d *DB) SetClientStatus(ctx context.Context, id int64, caps map[string]bool, lastErr string) error {
	b, _ := json.Marshal(caps)
	_, err := d.sql.ExecContext(ctx,
		`UPDATE client SET caps_json=?, last_error=?, last_seen_at=? WHERE id=?`,
		string(b), lastErr, now(), id)
	return err
}

func (d *DB) DeleteClient(ctx context.Context, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM client WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Library is one configured destination root.
type Library struct {
	ID           int64  `json:"id"`
	MediaType    string `json:"media_type"`
	Root         string `json:"root"`
	Enabled      bool   `json:"enabled"`
	StrictNames  bool   `json:"strict_names"`
	OneshotsDir  string `json:"oneshots_dir"`
	AcceptedExts string `json:"accepted_exts"`
}

func (d *DB) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT id, media_type, root, enabled, strict_names, oneshots_dir, accepted_exts
        FROM library ORDER BY media_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Library{}
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.MediaType, &l.Root, &l.Enabled,
			&l.StrictNames, &l.OneshotsDir, &l.AcceptedExts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveLibrary upserts on media_type: there is exactly one root per type,
// which is what makes the destination for a classification unambiguous.
func (d *DB) SaveLibrary(ctx context.Context, l Library) error {
	if l.Root == "" {
		return errors.New("store: library root is required")
	}
	_, err := d.sql.ExecContext(ctx, `
        INSERT INTO library(media_type, root, enabled, strict_names, oneshots_dir,
                            accepted_exts, created_at)
        VALUES(?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(media_type) DO UPDATE SET
            root=excluded.root, enabled=excluded.enabled,
            strict_names=excluded.strict_names, oneshots_dir=excluded.oneshots_dir,
            accepted_exts=excluded.accepted_exts`,
		l.MediaType, l.Root, l.Enabled, l.StrictNames, l.OneshotsDir,
		l.AcceptedExts, now())
	if err != nil {
		return fmt.Errorf("store: save library %s: %w", l.MediaType, err)
	}
	return nil
}

func (d *DB) DeleteLibrary(ctx context.Context, id int64) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM library WHERE id = ?`, id)
	return err
}
