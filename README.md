# Orphanarr

Finds completed downloads in your download clients that have **no category** — the orphans no
\*arr will ever import — identifies what media they contain, and files them where Plex,
Jellyfin, Navidrome, Komga, RomM and friends will actually see them.

[![CI](https://github.com/StevenGann/Orphanarr/actions/workflows/ci.yml/badge.svg)](https://github.com/StevenGann/Orphanarr/actions/workflows/ci.yml)

> **Status: v0.2, pre-release.** The loop is complete and usable from the web UI: connect a
> client, point it at libraries, scan, review each plan file by file, execute, undo. Dry-run
> ships **on**. Evaluate it on a small library before pointing it at 40 TB — not because
> anything is known to be wrong, but because nobody has run it against a real 40 TB library yet.

## The problem

In a normal \*arr stack, Sonarr and friends push downloads with a category set, then import them.
But downloads routinely end up in the client **without** one — manually added torrents, downloads
from a removed \*arr instance, migrated clients, sideloaded content, and media types that have no
\*arr at all (game ROMs, comics, audiobooks). Those pile up in the completed directory forever.
Nobody imports them. Nobody cleans them up.

## What it will not do

This list is the point of the project, not a disclaimer.

- **It never modifies, moves, deletes, renames, chmods or chowns a source file.** No config key
  enables it. The rule is enforced by the filesystem port, not by discipline: every mutating entry
  point funnels through one check, including the capability probe, which gets a narrow named
  carve-out at that same chokepoint rather than an exemption from it.
- **It never overwrites anything.** There is no overwrite collision policy — not as a default,
  not as an option. Publishing uses `link(2)`, which returns `EEXIST`, rather than `rename(2)`,
  which destroys an existing destination silently and with no error.
- **It never guesses.** `unknown` is a first-class outcome with a machine-readable reason. 15 of
  the 100 classification cases in the corpus expect exactly that, and that is the design working —
  a classifier that never refuses has simply moved its errors into your library. (`corpus_lint.py`
  prints both figures; this line quotes its output rather than restating it.)
- **It does not extract archives, write tags, rewrite archives, or call out to TMDB/TVDB.**

## Install

```yaml
services:
  orphanarr:
    image: ghcr.io/stevengann/orphanarr:latest
    environment: [PUID=1000, PGID=1000, UMASK=002, TZ=Etc/UTC]
    volumes:
      - ./config:/config
      - /mnt/pool:/data      # ONE mount, with downloads and media below it
    ports: ["8790:8790"]
    restart: unless-stopped
```

The API key is generated on first start and printed to the log **once**:

```
docker logs orphanarr | grep api_key
```

Then open `http://localhost:8790/?apikey=…`.

### One mount, or read-only mounts — pick one

Split `/downloads` and `/media` mounts make hardlinks impossible **inside** the container even when
they are one filesystem outside it, and the `st_dev` check everyone reaches for reports "fine" in
exactly that configuration. A read-only download mount fixes a different problem — it makes "never
touch a source file" a kernel guarantee — but a `:ro` bind is itself a separate mount, so it
forecloses hardlinking permanently.

Both are documented in `docker-compose.yml`, with the verification behind them in
`tests/verification/readonly_mount_test.sh`. Under the shipped copy-only default the read-only
topology costs nothing you are currently using.

## Configuration

Every setting lives in the database and is editable in the UI. Any of them can be pinned by an
environment variable, which wins at boot and renders the field read-only rather than letting you
edit something that will be overwritten.

| Key | Default | Notes |
|---|---|---|
| `ORPHANARR__OPS__DRY_RUN` | `true` | Ships on. Stays on until you turn it off deliberately. |
| `ORPHANARR__OPS__MODE` | `copy` | `copy`, `link_or_copy`, `link`. Anything but `copy` attempts a hardlink per (download root, library root) pair, but only where a real `link(2)` probe has passed. |
| `ORPHANARR__OPS__COLLISION` | `skip` | `skip`, `suffix`, `fail`. There is no `overwrite`. |
| `ORPHANARR__OPS__RESERVE_BYTES` | `10 GiB` | Floor; the effective reserve is `max(bytes, 5% of total)`. |
| `ORPHANARR__DETECT__AUTO_THRESHOLD` | `0.85` | Below this, or on any ambiguity, the item goes to review. |
| `ORPHANARR__DETECT__PDF_DEFAULT` | `review` | A bare PDF is genuinely undecidable between comic and ebook. |
| `ORPHANARR__SCAN__SETTLE_SECONDS` | `300` | A torrent that finished 30 seconds ago may still be moving bytes. |

## Development

```bash
go test ./...                                    # unit and corpus tests
go build -o orphanarr ./cmd/orphanarr
bash tests/e2e/roundtrip.sh ./orphanarr          # full API round trip vs a fake qBittorrent
python3 tests/verification/fs_semantics_test.py  # filesystem claims, real filesystems
python3 tests/verification/corpus_lint.py        # corpus well-formedness
```

`tests/corpus/` is the classifier's specification: 118 cases, at least a quarter of which expect a
*negative* result. A classifier that never refuses has simply moved its errors into your library.

`tests/e2e/roundtrip.sh` drives the whole thing over HTTP against a fake qBittorrent and
checksums the source file before and after a full execute. Every package refuses to touch a
source individually; that test proves the composition of all of them still does.

`tests/verification/` holds **40 numbered claims across four scripts, 46 checks in total**, run in
CI against real filesystems and real mount namespaces. (The two counts differ because the
bind-mount script's four checks are unnumbered and one numbered claim carries two assertions —
each script prints its own total, which is the number to trust.) If a runner cannot provide what a script needs
it exits 2 and CI records a visible warning — the skip is never silent, because a skipped mount
test is how "equal `st_dev` means you can hardlink" would have survived into this codebase.

## Design

`docs/BRIEF.md` is the stakeholder's requirements. `docs/DESIGN.md` is the ratified design — 2,600
lines, with an appendix of sourced facts and a dissent log recording every position that lost and
what would reopen it.

Two things there are worth reading even if you never touch the code:

- **§0** — the findings the whole design turns on, including several the qBittorrent wiki still
  documents incorrectly, and a note telling you to trust the citations over the counts because
  the document has been wrong about its own tallies six times — which is why every count in it
  is now printed by a script rather than typed into prose.
- **Appendix A** — the folklore this design deliberately contradicts, each entry with its source.

## Licence

MIT.
