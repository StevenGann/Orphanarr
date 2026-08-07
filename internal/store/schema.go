package store

// schema is applied in order. Migrations are forward-only and additive;
// each entry runs once and is recorded in schema_migrations.
//
// Column choices worth explaining are explained here rather than in a wiki,
// because the next person to read this file is the one who needs them.
var migrations = []struct {
	name string
	stmt string
}{
	{"0001_init", `
CREATE TABLE IF NOT EXISTS setting (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS client (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    -- kind is what the adapter factory switches on. A registry would buy
    -- the illusion of extensibility and an init-order bug; this is a
    -- two-line diff when a second product arrives.
    kind         TEXT NOT NULL DEFAULT 'qbittorrent',
    base_url     TEXT NOT NULL,
    username     TEXT NOT NULL DEFAULT '',
    password     TEXT NOT NULL DEFAULT '',
    api_key      TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 0,
    -- Capabilities are PROBED at runtime, per instance, never declared per
    -- product: a stock Deluge has no Label plugin and reports every
    -- torrent as uncategorised, which under O1 is the user's whole
    -- seeding library.
    caps_json    TEXT NOT NULL DEFAULT '{}',
    last_error   TEXT NOT NULL DEFAULT '',
    last_seen_at TEXT,
    created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS path_mapping (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id  INTEGER NOT NULL REFERENCES client(id) ON DELETE CASCADE,
    remote     TEXT NOT NULL,
    local      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_path_mapping_client ON path_mapping(client_id);

CREATE TABLE IF NOT EXISTS library (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type    TEXT NOT NULL UNIQUE,
    root          TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    strict_names  INTEGER NOT NULL DEFAULT 0,
    oneshots_dir  TEXT NOT NULL DEFAULT '',
    accepted_exts TEXT NOT NULL DEFAULT '',
    options_json  TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS orphan (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    -- RESTRICT, not CASCADE. Deleting a client used to silently destroy
    -- every orphan, plan and plan_step belonging to it — i.e. the entire
    -- undo history — at exactly the moment a user is most likely to want
    -- it. The API detaches orphans instead, so history survives.
    client_id      INTEGER REFERENCES client(id) ON DELETE SET NULL,
    -- external_id is OPAQUE. qBittorrent's happens to be an infohash;
    -- SABnzbd and NZBGet have none at all.
    external_id    TEXT NOT NULL,
    infohash       TEXT NOT NULL DEFAULT '',
    infohash_v2    TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL,
    save_path      TEXT NOT NULL DEFAULT '',
    -- content_fingerprint is the identity key sticky decisions hang off.
    -- It is computed from the manifest, so it works across clients and
    -- across products that share no identifier at all.
    fingerprint    TEXT NOT NULL DEFAULT '',
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'discovered',
    media_type     TEXT NOT NULL DEFAULT '',
    score          REAL NOT NULL DEFAULT 0,
    parse_conf     REAL NOT NULL DEFAULT 0,
    cardinality    TEXT NOT NULL DEFAULT '',
    reason         TEXT NOT NULL DEFAULT '',
    signals_json   TEXT NOT NULL DEFAULT '[]',
    parsed_json    TEXT NOT NULL DEFAULT '{}',
    files_json     TEXT NOT NULL DEFAULT '[]',
    -- first_seen_at drives the settle window. A torrent that completed
    -- thirty seconds ago may still be moving bytes.
    first_seen_at  TEXT NOT NULL,
    last_seen_at   TEXT NOT NULL,
    filed_at       TEXT,
    UNIQUE(client_id, external_id)
);
CREATE INDEX IF NOT EXISTS idx_orphan_state ON orphan(state);
CREATE INDEX IF NOT EXISTS idx_orphan_fingerprint ON orphan(fingerprint);

CREATE TABLE IF NOT EXISTS plan (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    orphan_id    INTEGER REFERENCES orphan(id) ON DELETE SET NULL,
    media_type   TEXT NOT NULL,
    library_id   INTEGER REFERENCES library(id),
    status       TEXT NOT NULL DEFAULT 'draft',
    -- Bytes are split so the UI can report "linked 40, copied 3" rather
    -- than an undifferentiated success.
    copy_bytes   INTEGER NOT NULL DEFAULT 0,
    link_bytes   INTEGER NOT NULL DEFAULT 0,
    warnings_json TEXT NOT NULL DEFAULT '[]',
    needs_review INTEGER NOT NULL DEFAULT 0,
    review_reason TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    executed_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_plan_status ON plan(status);

CREATE TABLE IF NOT EXISTS plan_step (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id      INTEGER NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    src_path     TEXT NOT NULL,
    dst_path     TEXT NOT NULL,
    -- method is what we INTEND; method_actual is what the syscall did.
    -- They differ whenever a link falls back to a copy, and permission
    -- handling is only legal on the branch that actually copied.
    method       TEXT NOT NULL DEFAULT 'copy',
    method_actual TEXT NOT NULL DEFAULT '',
    bytes        INTEGER NOT NULL DEFAULT 0,
    -- The source identity at plan time. Re-stat'ing against these before
    -- publish is invariant I13: #C29 shows a source mutated mid-copy
    -- yields a destination matching NEITHER its old nor its new contents,
    -- at the right size, after a successful fsync.
    src_dev      INTEGER NOT NULL DEFAULT 0,
    src_ino      INTEGER NOT NULL DEFAULT 0,
    src_size     INTEGER NOT NULL DEFAULT 0,
    src_mtime    TEXT NOT NULL DEFAULT '',
    digest       TEXT NOT NULL DEFAULT '',
    -- created_by_us is the flag rollback keys on. A path we did not create
    -- is never removed.
    created_by_us INTEGER NOT NULL DEFAULT 0,
    -- created_dirs_json records directories this step made, so rollback
    -- can remove them if empty and so directory chmod is permitted only
    -- on directories we own.
    created_dirs_json TEXT NOT NULL DEFAULT '[]',
    status       TEXT NOT NULL DEFAULT 'pending',
    error        TEXT NOT NULL DEFAULT '',
    UNIQUE(plan_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_plan_step_status ON plan_step(status);
CREATE INDEX IF NOT EXISTS idx_plan_step_dst ON plan_step(dst_path);

CREATE TABLE IF NOT EXISTS event (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         TEXT NOT NULL,
    level      TEXT NOT NULL DEFAULT 'info',
    -- code is a stable, documented vocabulary. The dashboard's
    -- skip-reason breakdown is built entirely from it.
    code       TEXT NOT NULL,
    client_id  INTEGER,
    orphan_id  INTEGER,
    plan_id    INTEGER,
    message    TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_event_ts ON event(ts DESC);
CREATE INDEX IF NOT EXISTS idx_event_code ON event(code);
`},
}
