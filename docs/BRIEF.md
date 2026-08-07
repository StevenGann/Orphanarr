# Orphanarr — Product Brief (Source of Truth for Requirements)

> This document captures the **stakeholder's stated intent**. It is the input to design,
> not the design itself. Agents may challenge, question, or seek clarification on anything
> here, but may not silently redefine it. Changes to this file require stakeholder sign-off.

**Stakeholder:** Steven Gann
**Date captured:** 2026-08-06

---

## 1. Problem Statement

Orphanarr fills a gap in the typical \*arr stack.

In a normal stack, Sonarr/Radarr/Lidarr/Readarr push downloads to a download client with a
**category** set, then import the completed download into the media library. But downloads
routinely end up in the client **without a category** — manually added torrents, downloads
from a removed/reconfigured \*arr instance, migrated clients, sideloaded content, media types
that have no \*arr at all (game ROMs, comics, audiobooks). These "orphans" pile up in the
completed directory forever. Nobody imports them. Nobody cleans them up.

Orphanarr finds those orphans, works out what kind of media they contain, and relocates them
into the right library in the right layout.

## 2. Core Behavior (as stated)

1. Connect to **multiple download clients**. qBittorrent support is the **minimum viable
   requirement** (the stakeholder runs multiple qBittorrent instances).
2. Poll for **completed** downloads that are **NOT tagged with a Category**.
3. For each orphan, **identify the media type** it contains:
   - Movies
   - TV shows
   - Music albums
   - Ebooks
   - Audiobooks
   - Comics
   - Game ROMs
4. **Relocate** the media to the appropriate destination for that media type.
5. Destinations must be laid out in a structure **suitable for the consuming media server**:
   - Movies / TV → Plex, Jellyfin
   - Music → Navidrome
   - Comics → Komga
   - Game ROMs → RomM
   - Ebooks / Audiobooks → (server TBD — Calibre-Web, Audiobookshelf, etc.)

## 3. Delivery & Operations (as stated)

- Distributed as a **Docker container**, built and published via **GitHub Actions**.
- Ships a **web interface** for configuration and monitoring, in the style of
  Sortarr / Huntarr / Cleanuparr and the broader \*arr ecosystem.

## 4. Explicitly Out of Scope (unless the stakeholder says otherwise)

- Orphanarr is **not** an indexer, downloader, or search tool. It does not acquire media.
- Orphanarr is **not** a replacement for Sonarr/Radarr/etc. It handles what they don't.

## 5. Open Questions for the Stakeholder

_Agents append here. Do not answer these on the stakeholder's behalf — flag them._

Consolidated from all five agents, Round 01 (2026-08-06). Deduplicated and ranked: the first four
**block or reshape design work**; the rest configure it.

### Blocking — ANSWERED 2026-08-06

All four answered by the stakeholder. `DESIGN.md` has **not** yet been revised to match; the
consequences below are recorded here so the revision round has a single source.

**Q1 — Are your download roots and your library roots on ONE filesystem, on ONE mount, inside the
container?**
**A1: No — assume a full copy for now.** Hardlinking is not a v1 assumption. Every placement is a
byte-for-byte copy, and disk consumption doubles for every orphan filed.
*Consequences:* §6.2's placement table inverts — copy is the primary path, hardlink an
opportunistic optimisation behind the §6.3 probe. Free-space preflight and `ENOSPC` become primary
failure paths, not edge cases. §8.3's "40 TB problem" gets materially worse. **And the hardlink
prohibitions relax**: a copy is a fresh inode with no relationship to the source, so `chmod`/`chown`
on Orphanarr's output is now safe and *fixes* §6.4's "#1 silent failure" (qBittorrent's `0600`
ownership rendering the library unreadable to Plex). See new Q25.

**Q2 — Which ebook and audiobook servers do you actually run?**
**A2: Audiobooks → Listenarr or Audiobookshelf. Ebooks → Komga, including PDFs.**
*Wrinkle 1:* **Listenarr is not a media server** — it is an \*arr-style *manager* (searches,
downloads, organises, hands grabs to qBittorrent/SAB/NZBGet), currently canary pre-release only,
not part of the official Servarr suite. So it is not an alternative layout target to
Audiobookshelf; it is an alternative to *Orphanarr* for that one media type, and it writes into an
Audiobookshelf-shaped library. **The audiobook layout target is Audiobookshelf either way** — §5.6
stands unchanged. See new Q27.
*Wrinkle 2:* Komga scans `cbz, zip, cbr, rar, pdf, epub`, so PDF ebooks are covered. But Komga's
ebook support is **EPUB-only for reflowable formats** — *"only EPUB format is supported. Other
formats will not be supported in the future."* MOBI/AZW3 orphans therefore have no destination.
See new Q26.
*Also:* comics **and** ebooks now both land in Komga (separate library roots — Komga forbids
overlapping library paths). This makes the `.pdf` tie-break (Q23) much cheaper to get wrong: a
misfile lands in the wrong library on the same server, not on a different server. **§5.7's proposed
`{Author}/{Title}/{Title}.{ext}` tree is Kavita/ABS-shaped and is not yet verified against Komga's
series derivation** — Komga's library guide does not document how it derives series from
directories. `[UNVERIFIED]` — the revision round must source this before §5.7 is enabled.

**Q3 — Are all your qBittorrent instances on the same host and filesystem as Orphanarr?**
**A3: The container mounts the download folders of the clients. Keep it simple for v1.0.**
Local mounts only; no seedbox, no remote instance, no network-copy design in v1.
*Consequences:* Separate per-client bind mounts **guarantee `EXDEV`**, which is consistent with A1 —
the two answers are coherent, not merely compatible. Per-client path mapping (S3) stays mandatory.
**Recommendation for the revision round:** mount every download folder `:ro`. Under copy-only that
costs nothing and enforces I1 (never touch a source file) in the *kernel* rather than by discipline
or by the `fsx.FS` port. → §11.1.

**Q4 — "Multiple download clients" (§2.1) — multiple qBittorrent *instances*, or other client
*products* (Deluge, Transmission, SABnzbd, NZBGet)?**
**A4: Both. qBittorrent only for now, but assume additional products later — users are likely to
run several heterogeneous clients covering different sources.**
*Consequences:* This **overrides §1.2's stated reasoning** for the "other clients" non-goal
("designing an abstraction from zero examples produces an interface shaped like exactly one
implementation"). Shipping remains qBittorrent-only, but the `client.DownloadClient` seam must now
be designed against a second client's semantics on paper. Three concrete pressures on the §2.3
interface, all of which assume qBittorrent's model:
- **Infohash is not a universal key.** SABnzbd and NZBGet are Usenet — there is no infohash. The
  identifier must become an opaque per-client string.
- **Not every client has a category concept.** Deluge's Label plugin is *optional*; with it
  uninstalled, **every torrent reads as uncategorised** — a catastrophic false-positive source for
  the §3.1 orphan predicate. Clients need a capability declaration, and a client that cannot
  express "has no category" must be refused rather than scanned.
- **`AddTags` is the only mutation in the interface and is not portable.** The "filed" marker needs
  a DB-only fallback for clients without tags.
See new Q28.

### Behaviour

**Q5 — Is "never move, never delete" acceptable?** v1 only ever *adds* — the download stays put and
keeps seeding. Where hardlinking is impossible that means a second copy on disk. Is that acceptable,
or must orphans eventually leave the completed directory — and if so, who deletes them?

**Q6 — Do you cross-seed** (cross-seed, autobrr, manual)? The overlap gate is the design's most
important safety check and it will send a meaningful fraction of your orphans to review rather than
auto-filing them. Better you know that now. If you definitely don't cross-seed, that gate can soften.

**Q7 — Is any \*arr running with a blank category field?** Sonarr's category is optional, and when
blank it polls qBittorrent with no category filter — meaning it and Orphanarr would watch the exact
same torrents. This decides whether read-only \*arr queue exclusion is needed in v1 rather than v1.1.

**Q8 — Are Sonarr/Radarr/Lidarr actively watching the destination libraries?** If so they may rename,
relocate, or (with unmapped-folder cleanup on) delete Orphanarr's output. Avoid \*arr-managed
libraries, or is coexistence expected?

**Q9 — Review-first, or fire-and-forget?** The proposed default is dry-run on, review-first, and
auto-filing only above 0.85 confidence. If you want zero-touch, the confidence gate becomes the
entire safety story and needs much harder testing.

**Q10 — On first run, consider all historical orphans, or only downloads completing after install?**

**Q10a — Should Orphanarr hand off to the \*arrs instead of filing directly, for the types that
have one?** For movies/TV/music that Radarr/Sonarr/Lidarr already monitor, Orphanarr could trigger
*their* import via their APIs and inherit a decade of parser hardening and metadata matching,
cutting our risk surface for that subset to near zero. It covers only those types and only
already-monitored titles — it does nothing for comics, ROMs, games, or anything the \*arr has never
heard of — so it cannot be the whole design. But it is a real option and the team should not decide
it for you by leaving it out.

**Q10b — When you delete a category in qBittorrent, does the torrent keep the dangling category
name, or does it become uncategorized?** §1 of this brief names "downloads from a removed or
reconfigured \*arr instance" as one of three orphan sources. If qBittorrent leaves the string
dangling, those torrents have a non-empty category, fail the orphan test, and are **invisible to
Orphanarr forever** — meaning the design covers two of the three sources you asked for. If it
clears, no work is needed. Nobody has verified this either way; one deleted category on a live
instance settles it.

**Q11 — What should happen to a torrent after Orphanarr files it?** v1 adds a tag and nothing else.
Setting a *category* is the natural "done" marker but can silently relocate your data under
Automatic Torrent Management. Tag only, or nothing at all?

### Environment & content

**Q12 — Roughly how many torrents per instance, and how many are uncategorized?** This is the
difference between "a review queue is fine" and "a review queue with 3,000 rows is a wall."

**Q13 — Which of the seven media types do you actually have orphans of right now, roughly how many
of each?** Equal design effort across seven types is how projects don't ship.

**Q14 — Which qBittorrent versions are in play?** ≥5.2.0 unlocks API-key auth (no CSRF juggling);
≥5.0 changed the state strings; <4.1 has no v2 API at all.

**Q15 — Are any library roots on SMB/CIFS, NTFS, exFAT, NFS, mergerfs, unRAID user shares, or ZFS?**
Changes hardlink availability, filename legality, and whether `rename` is atomic. unRAID's mover
also **breaks hardlinks silently** when it migrates a file between cache and array.

**Q16 — Is anime in scope?** Absolute episode numbering cannot be mapped to season/episode without a
lookup, so v1 routes it to review rather than guessing. Acceptable, or is anime important enough to
justify a TVDB dependency?

**Q17 — Are `.rar`-packed orphans common in your setup?** v1 parks them as `needs_extraction`
(Unpackerr's job). This decides whether that's right.

**Q18 — Anything you'd call media that isn't in the seven types?** Music videos, concerts, home
video, podcasts, courses/tutorials, magazines, sheet music, PC games (no listed server handles them).

**Q19 — Is the web UI LAN-only behind a reverse proxy, or internet-facing?** The UI lets an
authenticated user specify arbitrary destination paths for a process with broad filesystem access —
that is remote arbitrary file write, by design. This decides how hard v1's session hardening needs
to be.

**Q20 — Existing-library merge.** If `/media/tv/Severance` already exists with 40 episodes, should
Orphanarr add to it (the current assumption), or refuse to touch anything that already exists?

**Q21 — PUID/PGID or `user: "1000:1000"`?** v1 supports both; this is just which one the docs lead
with.

**Q22 — Plex-style `{edition-...}` tags: emit or not?** They need Plex Pass and PMS ≥1.28.1 and are
visible noise in Jellyfin. Default is off.

**Q23 — `.pdf` tie-break: comics, ebooks, or always ask?** Default is ask.

**Q24 — Who maintains this codebase, and in what language are they fluent?** The team chose Go on
deployment-footprint grounds. **If you write C#, .NET wins and the team withdraws** — a language you
can debug at 2am beats one the team likes. The *shape* (one process, one binary, SQLite, embedded
UI) matters more than the language.

### Raised by the answers to Q1–Q4 (2026-08-06)

**Q25 — Copy-only makes "never delete" expensive. Does that change your answer to Q5?**
Under hardlinking, filing an orphan cost ~0 bytes and "we only ever add" was nearly free. Under A1
every filed orphan **permanently doubles its own storage**, and the design still never deletes
anything. Two sub-decisions: (a) should Orphanarr refuse to file when destination free space would
drop below a threshold — and what threshold; (b) does copy-only push post-file source cleanup from
"a separate feature you may request later" into v1? *Design-team note, not a stakeholder question:*
copies being fresh inodes means Orphanarr **should** now set ownership/mode on its output — that is
the fix for §6.4's worst silent failure, and it is only available because of A1.

**Q26 — MOBI/AZW3 ebooks have no destination under a Komga-only ebook library.** Komga will never
support them. Park them in review as `unsupported_format`, classify them as `unknown`, or is a
second ebook library (Kavita/Calibre-Web) in play after all?

**Q27 — Do you actually run Listenarr, and if so does it set a category in qBittorrent?**
Two consequences, both real: (a) if it sets a category, its downloads are **not orphans** and
Orphanarr should never see them — but Listenarr is canary-only pre-release software, so if you
reconfigure or remove it, Q10b (does a deleted category leave a dangling string?) decides whether
its old downloads become invisible to Orphanarr forever; (b) if Listenarr is actively managing the
audiobook library, it may rename or relocate Orphanarr's output — this is Q8, scoped to audiobooks.

**Q28 — Which download client products do you actually expect to add, and in what order?**
A4 says "assume more later," which is enough to *shape* the seam but not enough to *validate* it.
The interface should be designed against one named second client, not against a hypothetical
average of all of them. Torrent-only (Deluge, Transmission) and Usenet (SABnzbd, NZBGet) pull the
design in different directions — Usenet breaks the infohash key, Deluge breaks the orphan predicate.

---

## 6. What "Done" Means for the Design Phase

A single `docs/DESIGN.md` that a competent developer could implement from, covering:
architecture, data model, media-type detection, destination layout rules, file operation
semantics, download-client abstraction, configuration schema, web UI scope, failure/rollback
behavior, packaging, and an explicit non-goals list. Approved by the team per
`team/PROCESS.md` (no more than one dissenter).
