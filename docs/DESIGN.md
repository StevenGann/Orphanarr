# Orphanarr — Design

> ## STATUS: RATIFIED — 2026-08-06. AMENDED — 2026-08-07.
>
> **Design vote (2026-08-06): 5 `APPROVE (with reservations)`, 0 `REVISE`.** Unanimous, against a
> threshold of ≤1 dissenter (`team/PROCESS.md` §2). Round 01 was 2 `APPROVE`, 3 `REVISE`.
>
> **Blocking-answers amendment (2026-08-07): 4 `APPROVE (with reservations)`, 1 `REVISE`.**
> Ratified. The stakeholder answered the four questions that gated implementation, and **one of
> the answers inverts a decision this document is built on**: placement is now copy-first, not
> hardlink-first. See the amendment changelog below and `team/rounds/round-02-blocking-answers-*`.
>
> Requirements: `docs/BRIEF.md` — the four blocking questions are **answered**; twenty behavioural
> and environmental questions remain, of which Q7 and Q25 are now blocking-class.
> Conflicts resolved and dissents recorded: **Appendix B**. Sourced facts: **Appendix A**.
>
> Ratified is not finished. Every agent has approved *with reservations* at every vote, and the
> reservations are applied in the changelog immediately following. **The document has now been
> wrong about its own tallies six times**, which is why §0 tells you to trust the citations over
> the counts — and why every count is now printed by a script rather than typed into prose.

### Amendment changelog — the four blocking answers, applied 2026-08-07

**The answers.** Q1: *assume a full copy for now.* Q2: *Audiobookshelf for audiobooks; Komga for
ebooks, including PDFs.* Q3: *the container mounts the clients' download folders; keep it simple
for v1.0.* Q4: *instances **and** products — qBittorrent first, but assume heterogeneous clients
later.*

**The consequence that outranks the rest, and it is arithmetic.** Max fileable bytes is
`free − reserve`, **permanently**: I1 forbids deleting sources and I2/D13 forbid deleting
destinations, so nothing ever returns space. Filing completes only when `total ≤ free − reserve` —
precisely the condition that fails for the user who needs this tool. **Under copy-only plus
never-delete, v1 cannot terminate on a realistically-full array.** §8.3's own 38.4 TB example is
2.2 days of wall-clock at 200 MB/s and 14.8 days at 30 MB/s on §2.5's serialized executor. This is
not a defect in any section; it makes BRIEF §5 Q25(b) the question of whether v1 works, and it is
the stakeholder's to answer.

**Placement inverts, but the mechanism is kept.** Copy is the primary path; hardlinking is an
opportunistic optimisation that auto-upgrades **per (download root, library root) pair** as the
§6.3 probe passes, and the upgrade is surfaced rather than performed silently. One agent argued
for cutting the hardlink path outright and lost 4–1 — recorded as **D15**, on the reading that
*"assume a full copy for now"* is a planning assumption, not a statement about physics.
**§6.5's publish primitive is not part of this inversion**: it is `link(partial, dst)` in the
destination directory, unaffected by mount topology, and it is the sole reason I2 is mechanically
true. Three agents blocked round 01 on that step; it must not be "simplified" back to `rename(2)`.

**Permissions invert with it, and this fixes §6.4's self-declared "#1 silent failure."** A copy is
a fresh inode, so modes can be set without touching a seeding torrent — proven `#C21`, the exact
converse of `#C19`. Modes are set on the partial *before* publish, which the hardlink path
structurally cannot reach. **No `chown`** (`CAP_CHOWN`, `#C23`, contradicts D3). New invariant
**I12**, scoped to non-directories because a directory's `st_nlink` is never 1.

**A failure class that did not exist under hardlinking.** `link(2)` never opened the source; a copy
holds a live file in a running client's storage open for hours. `#C29` produces a destination
matching **neither** the source's old nor its new contents, at the right size, after a successful
`fsync`. Size and `fsync` cannot detect it. → post-copy re-`stat`, invariant **I13**.

**Free space was wrong in four independent ways** — keyed on root path rather than filesystem,
never re-checked, an absolute 1 GiB reserve, and the wrong `statfs` field (`#C28` measured 11.61 GiB
of root-reserved space `f_bfree` reports and we can never have). All four were invisible while
`copy_bytes` was usually zero.

**Ecosystem corrections from source.** Komga creates a Series only for a directory that *directly
contains* a book file, so §5.7's `{Author}/{Title}/{Title}.ext` yields one single-book series per
book — replaced. Komga's book comparator is **natural**, so §5.4's stated reason for zero-padding
is wrong (the rule survives; the reason does not). **RomM's scanner is an exclusion list, not an
allowlist** — alone among the six servers it indexes `.orphanarr-partial`, now renamed
`.orphanarr-partial.tmp`. A stock Deluge has no Label plugin and reads **every** torrent as
uncategorised → invariant **I14**.

**Read-only download mounts are not free** (`#R2`): a `:ro` bind is a separate vfsmount, so it
guarantees `EXDEV` and forecloses hardlinking permanently. Two topologies, stated as a choice.

**One defect was introduced by the distillation and caught at the vote** — an unscoped I12 that
forbade `chmod` on every directory. It existed in none of the five proposals. Recorded because
`PROCESS.md` §4 is right that the record of being wrong is the most useful thing in these files.

### Round-03 changelog — the round-02 vote's reservations, applied

**Two defects round 02 introduced.** `mixed` (tie-break rule 8) was **dead code**: rule 4 matched
every video-bearing payload unconditionally and ran first, so `Show S01 + OST` resolved to TV and
stranded the album — the exact failure rule 8 was added to prevent. Rule 4 now carries a 30%
qualifier. And §5.2's date-based TV rule **named a file it could not place**: Plex's own example
puts dailies in `Season 08`, a real season number the date does not yield, so these now route to
review alongside anime absolute numbering rather than auto-filing.

**Three corrections that were themselves wrong.** §3.7's `GET /` probe works, but not because CSRF
is skipped for the web root — both checks run before path dispatch; it works because we send
neither header, which means **the diagnostic is only valid while header injection is off**. §3.7's
"the whitelist defaults to `*`" understated the risk: host validation checks the **port first**,
so a proxy fronting 8080 on :443 is rejected with the whitelist untouched. And §0's corrected
agent tally was itself a second wrong count.

**Two hazards in the round-02 fixes.** Go's `http.Client` re-adds `Referer` on redirect-followed
requests, defeating "send neither" and misreporting through the new diagnostic — `CheckRedirect`
must strip it. And `link`+`unlink` leaves two names for a window, so `Reconcile()`'s partial sweep
had to become unconditional rather than an `elif` (found independently by two agents).

**Scope corrections.** §5.1's inline-extra suffix exclusion was Plex-only, two bullets after the
document argues that taking one server's list mishandles the other's — now the union, expressed as
(separator, token) pairs. §5.5's "the folder name is the enum key" invited transcribing `PSX`
instead of `psx`. §5.8's 255-byte budget ignored the 18-byte partial suffix. The no-cascade rule
named `plan` but not `classification`. I6's containment was purely lexical.

**Stale summaries.** Appendix B's D4 still described the round-01 recipe — *"+ rename only"* — the
precise hazard three agents blocked on. D8 still called the music tag read "free". Both fixed;
the general lesson is recorded in D4.

**Also:** `Parsed.Confidence` given a home in the struct and the schema; skip-reason codes added so
§9's dashboard can answer *"why isn't it doing anything"*; `POST /plans/{id}/undo` added; webhook
taken off the executor's critical path; unRAID's mover downgraded to `[PARTIAL]` and cited; the
API-key gate moved off a `webapiVersion` bound no shipped release exercises.

### Round-02 changelog

**Blocking objections (all three applied).** The three `REVISE` votes were effectively one
finding: the draft forbade destroying an existing destination file and then specified two ways to
do it.

1. **§6.5 — copy no longer publishes with `rename(2)`.** `rename` silently destroys an existing
   destination (`#C9`), and the plan-time collision check is separated from the publish by the
   whole length of the review queue. Publish is now `link` → `unlink`, falling back to
   `renameat2(RENAME_NOREPLACE)`, then to a re-checked `rename` with a recorded warning.
   *(Senior Dev and Old Man, independently, same remedy.)*
2. **§6.6/§8.2 — `keep_larger` removed.** It contradicted I2 and the paragraph printed beneath it.
   Logged as D13. *(Devil's Advocate.)*
3. **§6.6 — the destination is re-evaluated by the executor immediately before each step**, not
   only at plan time. *(Senior Dev.)*

**Factual corrections.** §3.7's `Referer`/`Origin` advice was **backwards** — sending neither is
the permissive path, and injecting them creates the failure behind a reverse proxy; rejection is
401, not 403 (found independently by the Arr Expert and the Fact Checker). §3.8's state-aliasing
claim was master-only and never shipped, and Web API `2.11.0` is not a shipped version number.
§5.5's RomM slug story was wrong in a way that could mislead an implementer into transcribing
`ps1`. §5.1's Plex extras list omitted `Interviews`/`Scenes`/`Shorts`/`Other` and included `Extras`
which is Jellyfin's, and the sidecar rename rule would have promoted a featurette into a second
main movie. §3.2's `rootPath()` condition covered one of its two cases.

**Corrected overclaims.** §0 and §2.4 asserted unanimity that the round-01 record does not
support; both now state the real counts, including where two agents proposed the exact folklore
the design refutes. Appendix B's D2 tally was wrong (4/5, not 3/5) and D5 misdescribed the
position it rejected.

**Restored or newly specified:** date-based TV layout, mixed-type parking (tie-break rule 8),
`.bin`+`.cue` ROM-vs-music tie-break, parse-confidence gating in I9, the event-code vocabulary,
the webhook spec, the REST endpoint list, `client_write: none | tag`, the I10 probe carve-out,
the `plan`-must-not-cascade rule, unRAID's mover, and the Komga RAR5/`convertToCbz` conditions.
Three previously-unrecorded conflicts are now D12–D14.

---

## 0. How to read this document

Five agents produced independent proposals without seeing each other's work. On the decisions
that matter most — what an orphan is, what may touch the filesystem, what happens when we don't
know — they converged hard. Where they didn't, Appendix B names the conflict, the ruling, and
the position that lost.

Two findings dominate everything below:

1. **`content_path` is not a safe unit of work.** For a multi-file torrent whose files have no
   common parent, qBittorrent returns the *entire shared save path*. Treating that as "the
   orphan" operates on every other download in the directory. → Invariant **I3**.
   *Reached independently by four of the five agents. The fifth built its predicate on
   `content_path` and exhibited the trap rather than finding it.*
2. **`st_dev` equality does not mean you can hardlink.** The kernel compares vfsmount pointers,
   not superblocks; two Docker bind mounts of one filesystem report identical `st_dev` and still
   return `EXDEV`. The check every developer reaches for returns "safe" in exactly the
   misconfiguration that fails. → §6.3.
   *This one was settled by nobody's agreement. Three agents wrote the caveat that `st_dev`
   equality is necessary but not sufficient — and then **four of five gated on device equality
   anyway** somewhere in their own pseudocode, including two of the three who wrote the caveat.
   One agent went further and asserted the exact opposite of what the test shows. It was settled
   by running `link(2)` in a user namespace and watching it fail with identical `st_dev` on both
   sides.*

3. **Nothing ever returns disk space, so the tool has a hard ceiling.** Added by the 2026-08-07
   amendment. Sources are never deleted (I1) and destinations are never overwritten (I2/D13), so
   the most Orphanarr can ever file is `free − reserve` bytes — once. Under copy-only that ceiling
   binds immediately rather than theoretically. Every free-space rule in §6.5 and §10.2 exists
   because of it, and BRIEF §5 Q25 is the stakeholder decision it forces.

Neither of the first two is in the qBittorrent wiki or the TRaSH guides in those words. All three
rest on project source, arithmetic, or a passing test in `tests/verification/` — **not** on how
many agents agreed.

**Finding 2 is now load-bearing in a different way than when it was written.** The 2026-08-07
amendment makes copy the primary path, so `st_dev` is no longer the gate for the *common* case —
but it is still the trap for anyone who reads "Orphanarr copies" and concludes hardlinking was
never possible. It also acquired a second, *legitimate* use in the same amendment: `st_dev`
identifies the destination **filesystem** for space accounting (§6.5), and it is one leg of the
`(dev, ino)` identity test that catches the same physical file reached through two mounts (§3.1
O10). **Never as a linkability gate; correct for accounting and for identity.**

That last clause is load-bearing, and this paragraph has now been wrong twice. The round-01 draft
claimed both findings were reached "independently by all five agents"; round 02 corrected it to a
different wrong count; the counts above are round 03's, and even they were disputed between two
agents whose greps disagreed (`st_dev` does not match `syscall.Stat_t.Dev`). **Treat every agent
tally in this document as weaker evidence than the citation beside it.** `team/PROCESS.md` §4 is
right that the record of being wrong is the most useful thing in these files, and this is that
record.

---

## 1. Scope & non-goals

### 1.1 v1 ships

| # | Capability |
|---|---|
| S1 | qBittorrent WebUI API v2 client, **N instances**, cookie auth + Bearer API-key auth (≥5.2.0) |
| S2 | Orphan discovery with the conservative predicate in §3.1 |
| S3 | Per-client **path mapping**, mandatory, verified before a client can be enabled |
| S4 | Offline classification into the seven brief types + `unknown`, with persisted evidence |
| S5 | Destination layout for Plex, Jellyfin, Navidrome, Komga, RomM, Audiobookshelf |
| S6 | **Verified-copy** placement, journalled, rollbackable, with opportunistic hardlinking per (download root, library root) pair where the §6.3 probe passes |
| S7 | Dry-run **on by default**; review queue; per-plan undo |
| S8 | Web UI (six screens) over a JSON API |
| S9 | SQLite state; multi-arch Docker image published by GitHub Actions |

### 1.2 v1 explicitly does not ship

| Non-goal | Reason |
|---|---|
| Download clients other than qBittorrent | **Reason amended 2026-08-07.** BRIEF §5 A4 refutes the original *"the need is multiple instances, not multiple products"* — the stakeholder expects heterogeneous clients later. What survives, and is load-bearing: **designing an abstraction from zero examples produces an interface shaped like exactly one implementation.** We still cannot name the second client (BRIEF Q28), so v1 ships one adapter while §2.3's seam is *shaped* for others — opaque `ExternalID`, runtime-probed capabilities, optional `MarkFiled`. Deluge (labels via an optional plugin, defaulting to none — I14) and SABnzbd (categories mandatory-with-a-default, so "no category" barely exists) remain the concrete evidence that the semantics differ materially. |
| Remote or seedbox download clients | BRIEF §5 A3: the container mounts the clients' download folders, and v1 keeps it simple. A client whose payload is not on a locally-reachable filesystem cannot be copied *or* linked from, and a network-copy design is a different feature. Already encoded by O8; documented here so it is a decision rather than an omission. |
| External metadata lookup (TMDB/TVDB/MusicBrainz/ComicVine/IGDB/Audible) | **Unanimous across all five agents.** Plex and Jellyfin match on title+year and series+SxxEyy — which the release name already carries. For four of the seven types a lookup accomplishes nothing at all: Navidrome reads tags, RomM matches filenames, Komga reads `ComicInfo.xml`, Audiobookshelf parses folder names. A wrong TMDB match is *confidently* wrong and gets baked into the path. See §4.6. |
| Archive extraction (`.rar`/`.7z` sets) | Unpackerr solves this. Archive-only payloads classify as `needs_extraction` and stop. Extraction also breaks the disk-space model. |
| Writing tags or rewriting archives | **Reason restated 2026-08-07 — the ruling stands, its original argument does not.** The inode argument (*"the library file and the seeding file are one inode"*, `#C5`) is true only of a hardlink, and copy is now the primary path. **The ruling survives on scope**, independently supported: §5.3 places music fully verbatim because Navidrome reads embedded tags and a wrong write is worse than no write, and §5.5 says the same for ROM archives. Recorded explicitly because a live decision defended by a dead argument is how the next round makes a bad call — and because §5.3's tag-driven server makes *"just write the tags"* otherwise unanswerable. Still forbidden on a **link**, where `#C5` applies in full. |
| `chmod`/`chown` on **hardlinked** files | Permissions live on the inode. Proven: `#C19`; converse proven `#C21`. Enforced mechanically by **I12** and by `fsx.FS`, which refuses `Chmod`/`Chown` on any **non-directory** path with `st_nlink > 1`. Orphanarr sets modes on directories it creates and on the `.orphanarr-partial.tmp` of files it **copied**, before publish — never on a link, and never on a published path. **`chown` is not performed at all**: it needs `CAP_CHOWN` (`#C23`), which contradicts D3's first-class `--user 1000:1000`. |
| Moving or deleting source files | §6.1. No config key enables it in v1. |
| `setCategory`, `setLocation`, `delete`, `stop`, `recheck` | Each has a documented destructive side effect. §3.6. |
| Writing into a Calibre library directory | Calibre's manual forbids it outright. §5.7. |
| ffprobe / ffmpeg | A ~70–150 MB dependency to answer one question that container headers answer for free. §4.3. |
| Splitting mixed-content torrents across libraries | Detected, parked, surfaced. Splitting has its own rollback story. |
| Multi-user auth, RBAC, OIDC/LDAP, notification *providers* (Discord/Telegram/Gotify/Apprise), calendar, charts, i18n | Single admin. One generic outbound webhook (§10.5) is the whole notification story. |

Prometheus `/metrics` is **not** a non-goal: it ships behind a flag, off by default (§10.4).

---

## 2. Architecture

### 2.1 Stack: Go 1.24+, `CGO_ENABLED=0`, single static binary

Reasoning, not preference:

1. **The product is a filesystem product.** Its correctness lives in `link(2)`, `rename(2)`,
   `statfs`, and in distinguishing `EXDEV` from `EPERM` from `EMLINK` from `ENOSPC` — four
   different user-facing problems with four different remediations. Go exposes all of them in
   the standard library with no FFI. (.NET had no public hardlink API until
   `dotnet/runtime#69030`, closed 2025-10-22 against milestone 11.0.0, so older TFMs need
   P/Invoke for the single most important operation in the program.)
2. **Distribution.** `CGO_ENABLED=0` cross-compiles `linux/arm64` from an amd64 runner with **no
   QEMU**, into a small single-binary image. This audience runs Synology, unRAID, N100s and Pis.
   *(The commonly-quoted figures — ~25 MB image, ~90 s versus 20–30 min emulated — are
   `[UNVERIFIED]`: nobody on this team built either image. The decision does not rest on them;
   it rests on point 1 and on the .NET hardlink gap, both of which are sourced.)*
3. **`modernc.org/sqlite`** (pure Go) keeps `CGO_ENABLED=0` true, which is what keeps both of the
   above intact.

Ecosystem precedent does not decide this and was not used to: the \*arr suite is C# because it
began as NzbDrone in 2010; Unpackerr and autobrr are Go; Cleanuparr is C#. Ecosystem consistency
is a UI and API-shape property that users perceive, not a runtime property.

### 2.2 Topology

One process. One container. One SQLite file. One embedded UI. No queue, no broker, no service
split — the entire workload is "poll a few HTTP endpoints, then move files on one array, one at
a time."

### 2.3 Packages and seams

```
cmd/orphanarr/                  wiring only
internal/
  config/     env + DB settings, validation
  store/      SQLite: schema, migrations, typed queries. The ONLY package importing sql.
  client/     DownloadClient interface
    qbittorrent/                the one implementation; the ONLY code that knows /api/v2
  pathmap/    remote→local translation, longest prefix wins, per client
  scan/       polling, orphan predicate, stability gate, overlap index
  inspect/    resolves the file manifest; stat; fingerprint
    probe/    pure-Go tag + container-header reader (no external binaries).
              THE ONLY code in the classification path that touches a file.
              Its results are materialized into the FileSet. §4.2 S3.
  classify/   PURE: (FileSet, Rules) -> Classification.  No I/O, no clock, no network.
    release/  release-name grammar
  layout/     PURE: (MediaType, Parsed, []SourceFile, LibraryConfig) -> []PlannedFile
  plan/       layout + preflight -> Plan.  Inert, serializable value.
  exec/       the ONLY package that writes to the media filesystem
  fsx/        filesystem port + real impl + fault-injecting test impl
  api/        REST /api/v1, X-Api-Key
  web/        //go:embed of the built SPA
```

**Three seams, each with the concrete variation that justifies it:**

1. `client.DownloadClient` — justified by BRIEF §2.1 and **reshaped 2026-08-07 by BRIEF §5 A4**
   ("instances *and* products"). Shaped by what we need, not by a union of every client's
   features. Note what is *absent*: no `Pause`, `Delete`, `Recheck`, `SetLocation`,
   `SetCategory`. Those are forbidden by **I8**, so they are not in the interface.

   ```go
   type DownloadClient interface {
       ID() string
       Probe(ctx context.Context) (ClientInfo, error)   // version, auth mode, Capabilities
       ListItems(ctx context.Context) ([]Item, error)   // ALL items — see §3.3
       ListFiles(ctx context.Context, id ExternalID) ([]ItemFile, error)
       MarkFiled(ctx context.Context, id ExternalID, marker string) error   // the ONLY mutation
   }

   type ExternalID string        // opaque. qBittorrent's is the infohash; Usenet has none.

   type Item struct {
       ID       ExternalID
       Category *string          // nil = this client CANNOT express categories. See I14.
       Complete bool             // the adapter absorbs O3-O6; scan/ keeps only policy
       // ...
   }

   type Capabilities struct{ Categories, Tags, FileList bool }   // runtime-probed, per INSTANCE
   ```

   **Four changes, and each is here because deferring it costs a migration or an API break.**

   - **`ExternalID` is opaque.** SABnzbd and NZBGet have no infohash. §7 already makes
     `content_fingerprint` — computed from the manifest — the identity key that sticky decisions
     hang off, so nothing depends on infohash *semantics*; this is a mechanical rename plus a
     five-table primary-key migration, free now and expensive after there is code.
   - **`Category *string`, where nil means "cannot express categories" means never an orphan.**
     Go's zero value for a pointer is nil, so **a careless adapter files nothing.** The default
     wrong answer is the safe one. See I14.
   - **`Capabilities` is probed at runtime, per instance — never declared per product.** A static
     `deluge: {categories: true}` is right on a configured instance and catastrophic on a stock
     one, where `enabled_plugins` defaults to `[]`
     (`deluge/core/preferencesmanager.py:82`) and every torrent reads as uncategorised.
   - **`MarkFiled` replaces `AddTags`** and may return `ErrUnsupported`; `client_write: none`
     already exists as the fallback. **A marker failure never fails a plan or triggers rollback** —
     a rule that is correct today and becomes necessary with a second client.

   **Deliberately deferred, with the finding kept:** `Capabilities.ManifestSource`. A Usenet
   client's completed job is whatever post-processing left on disk, which forces exactly the
   `WalkDir` that I3 forbids; such a client must inherit the rootless-torrent posture of D10. The
   *field* costs neither a migration nor an API break, so it waits for the client that needs it.

   **Not built:** a second adapter, a stub adapter, a plugin registry (`switch kind` on the
   `client.kind` column §7 already has is a two-line diff; a registry buys the illusion of
   extensibility and an init-order bug), dynamic loading, an adapter SDK, capability
   *negotiation*, or an abstracted conformance suite — extraction from two examples is
   refactoring; abstraction from one is fiction. A CI check (`go list -deps` plus a token grep)
   enforces that no qBittorrent type escapes the adapter, because the seam stays clean only while
   something enforces it.

2. `fsx.FS` — justified by a *present* need, not a future one: `EXDEV`, `ENOSPC`, `EACCES` and
   crash-mid-copy cannot be tested reliably against a real disk, and those are the three bugs
   that eat someone's library. The fault-injecting implementation is the point.

3. `classify` and `layout` are **pure functions**. No context-carried DB, no logger, no clock.
   This is what makes `tests/corpus/` — 118 cases and growing — run in milliseconds with no
   filesystem, which is what makes it get *run*.

   **Purity is preserved by making probing a separate pass, not a callback.** Reading a FLAC
   header is I/O, so `probe/` lives under `inspect/`, and `Classify` runs at most twice: once
   over the cheap `FileSet`, and — if the first pass lands below the auto threshold, or the
   payload is audio (§4.3) — once more over a `FileSet` enriched with probe results.
   `Classify` itself never opens a file, and every corpus case is a fully-materialized
   `FileSet` including its probe fields.

**Seams deliberately not built:** no plugin registry for media types (seven types, an enum and a
`switch` — the variation between them is not symmetric and a uniform interface would be shaped
like movies with five awkward exceptions); no rules DSL; no DI container; no ORM; no
`sync/maindata` RID machinery.

### 2.4 The pipeline, and the boundary that matters

```
poll → predicate → resolve manifest → inspect → classify → layout → plan → [approve] → execute → verify
                                                                            └─ dry-run stops here
```

**`plan` ⟂ `exec` is the load-bearing boundary.** A `Plan` is an inert, serializable value
containing every resolved destination path, the chosen operation per file, byte totals, and every
collision determination. Dry-run is **not a flag inside the executor** — it is *not calling the
executor*. This makes dry-run structurally incapable of diverging from real behaviour, which is
the failure mode of every dry-run implemented as `if !dryRun { doIt() }`.

All five agents reached this principle independently — one code path, no possibility of drift. It
is the single most agreed-upon structural decision in the round. On the **mechanism**: three
specified "don't call the executor", one specified `Execute(ctx, plan, commit=false)` — the
flag-inside-the-executor shape this section rejects, for the same stated reason — and one
specified only the property (the same `Plan` object either way) without naming a mechanism. The
principle is unanimous; the mechanism is a 3–1–1 ruling, logged as **D12**.

### 2.5 Concurrency

- One goroutine per client, independent tickers, independent backoff, independent circuit breaker.
- **One executor. Serialized. One plan at a time, one step at a time.** The work is sequential
  I/O against one array; parallel copies are *slower* and multiply the interleaved failure states
  recovery must reason about. Every step is independently journalled, so a bounded worker pool is
  a contained change if anyone ever measures a win on NVMe.

---

## 3. Download client integration

### 3.1 The orphan predicate

A torrent is an **orphan candidate** iff all of the following hold. Any one failing means we do
not touch it.

| # | Condition | Notes |
|---|---|---|
| O1 | `category == ""` | Exactly the empty string. `category` is always a string, never null or absent. **Categories are hierarchical** (`movies/4k` is legal) — do not treat an unfamiliar category as uncategorized. |
| O2 | Torrent has metadata (`len(files) > 0`) | Without it `content_path` and `root_path` are `""`, and `join(root, "")` silently returns `root`. |
| O3 | `progress >= 1.0` **and** `amount_left == 0` | Both, not either. |
| O4 | `state` ∈ `{uploading, stalledUP, queuedUP, forcedUP, stoppedUP, pausedUP}` | An explicit allowlist. See §3.3 for why `filter=completed` is not usable. |
| O5 | Every selected file (`priority != 0`) has `progress == 1.0` | `progress` is computed over *wanted* bytes. A season pack with 9 of 13 episodes deselected reports `progress: 1.0, amount_left: 0`. |
| O6 | No resolved path ends in `.!qB` | The incomplete-file marker. Its presence on a "complete" torrent is a contradiction: skip and log. |
| O7 | `now - first_seen_at >= settle` (default 300 s) | **Keyed on Orphanarr's own clock, not `completion_on`** — see §3.4 FP-4. |
| O8 | Every selected file resolves to an existing local path after mapping, with matching size | An unmapped path is an **error**, never a pass-through. |
| O9 | No resolved path lies inside a configured **library** root | Users seed back from their libraries. Filing those restructures the library under itself. |
| O10 | No resolved path overlaps a **categorized** torrent on **any** configured client, where overlap is **path-equal OR `(dev, ino)`-equal OR `content_fingerprint`-equal** | The cross-seed gate. §3.4 FP-1. Union, not replacement — see below. |
| O11 | Not excluded: ignore tag, ignore save-path glob, ignore tracker host, or a sticky user `ignore` decision | Tags are joined with `", "` — **comma and space**. A naive `split(",")` leaves leading spaces and the opt-out silently never matches. |

**Content stability gate.** Across two consecutive polls ≥60 s apart, the resolved manifest's
total size and max mtime must be unchanged. This catches the incomplete→complete relocation that
`completion_on` misses, and catches a user copying files into the tree by hand.

**Re-check immediately before executing.** A torrent's category can change between the scan and
the job (an \*arr's post-import category, a user's edit). If `category != ""` or `state` has left
the allowlist at execute time, abort.

### 3.2 The rule that outranks everything else in this section

> **Orphanarr never operates on `content_path`.**

`content_path` is documented as *"root path for multifile torrents, absolute file path for
singlefile torrents"*. The source has a third branch the docs omit:

```cpp
Path TorrentImpl::contentPath() const {
    if (!hasMetadata()) return {};
    if (filesCount() == 1) return (actualStorageLocation() / filePath(0));
    const Path rootPath = this->rootPath();
    return (rootPath.isEmpty() ? actualStorageLocation() : rootPath);   // ← HERE
}
```

`rootPath()` is `Path::findRootFolder(filePaths())`, which returns empty in **two** cases, not one:

- any file has ≤1 path element — i.e. it sits at the torrent's top level; **or**
- any file's first path element differs from the others — i.e. the torrent has two or more
  distinct top-level directories, which is what many season packs and discographies look like.

In either case `content_path == save_path == the entire shared completed directory`, which may
hold dozens of other torrents. This is not exotic, and the second case is considerably more common
than the `NoSubfolder` setting people usually think of.

**Instead:** call `GET /api/v2/torrents/files?hash=<h>`, join each `name` (relative path) to the
mapped `save_path`, and compute the common prefix ourselves. `content_path` is displayed in the
UI as an informational field and used for nothing else.

*One assumption in that join is unverified and worth a cheap test rather than an argument:*
qBittorrent builds real paths from `actualStorageLocation()`, while `save_path` serializes the
*configured* location (`isAutoTMMEnabled() ? categorySavePath(category()) : m_savePath`). These
agree in the steady state; nobody has shown they always agree for AutoTMM-managed or migrated
torrents — which is exactly this product's population. **O8 makes the failure mode "found
nothing", not data loss.** The settling test, added to `qbittorrent_contract_test.py --live`:
assert `dirname(root_path) == save_path` for every torrent that has a root folder, and
`content_path == save_path + "/" + files[0].name` for every single-file torrent.

A torrent whose computed common prefix resolves to a mapped save path, a configured download
root, or `/` is **rootless**: it is flagged, it operates only on its enumerated files, it never
performs a directory-level operation, and its destination folder name requires human confirmation.
It never auto-files.

`torrents/files` is also the authoritative manifest for a second reason: a `filepath.WalkDir`
would sweep up `.parts`, `.!qB`, and anything else sharing the directory.

### 3.3 Fetching: `filter=all`, filter locally

We do **not** use `filter=completed` and we do **not** use the server-side `category=` filter.

`filter=completed` maps to `TorrentFilter::Completed → torrent->isCompleted()`, which is
*state-derived* and returns true for `CheckingUploading` — a torrent mid-recheck, whose files are
in flux. `filter` semantics have also drifted between releases (`filter=paused` returned all
elements in 5.0).

The `category=` filter is correct, but using it is incompatible with **O10**: the cross-seed
overlap index needs *every* torrent, including categorized ones. A second call would be waste. A
homelab with 5,000 torrents produces a few MB of JSON every 15 minutes. That is free. Correctness
is not.

**O10's three-legged overlap test, amended 2026-08-07.** BRIEF §5 A3 says the container mounts the
clients' download folders — *plural*. Two clients sharing one host directory, mounted twice, produce
**one physical file and two resolved path strings**: a path-equality test finds no overlap, I4's
collapse never fires, and FP-1 — the class ranked highest for both likelihood and damage — fires
cleanly. Under hardlink-first that cost 0 bytes; under copy-only it costs a full duplicate.

- `(dev, ino)` equality catches exactly that case, at **zero extra syscalls** — O8 already stats.
  Note the symmetry with §6.3: the same kernel behaviour that refutes `st_dev` as a *linkability*
  gate makes it sound as an *identity* gate.
- `content_fingerprint` equality is the third leg because the second one **narrows the hole rather
  than closing it**: every alias crossing a FUSE boundary — unRAID `/mnt/user` vs `/mnt/disk1`,
  mergerfs pool vs branch — has `st_dev` differing *by construction*, so legs one and two fail
  together. Both are named target platforms (BRIEF §5 Q15; §6.3 and §6.7 already discuss the unRAID
  mover). The fingerprint is already computed, already a §7 column, path- and inode-independent,
  and fails toward review rather than toward filing.
- Union, never replacement: NFS and mergerfs inode stability is `[UNVERIFIED]`.

BRIEF §5 Q37 asks whether two clients actually share a download directory — it decides whether this
is a safety net or a routine event.

**Where the overlap index gets its file paths, since `torrents/info` does not return them.** O10
compares *resolved file paths*, so the index needs a manifest for categorized torrents too. Two
mechanisms, version-gated:

- **≥5.2.0:** `GET /api/v2/torrents/info?includeFiles=true` serializes each torrent's file list in
  one request. Verified present in `release-5.2.0`/`5.2.1`, absent in `5.0.0` and `5.1.x`.
  Conveniently, 5.2.0 is also the API-key floor (§3.7).
- **<5.2.0:** N calls to `torrents/files`, cached by `(client_id, infohash)`. **Invalidate on a
  size change, a state change, a save-path change, *and* a TTL** — `torrents/renameFile` and
  `renameFolder` change a manifest's relative paths with none of the first three, and a stale
  index means O10 misses an overlap, which is the highest-damage false-positive class in §3.4.
  Manifests are otherwise stable, so the steady-state cost is near zero.

Two shapes an implementer must not assume away: `includeFiles` is guarded by
`if (includeFiles && torrent->hasMetadata())`, so the `files` key is **absent**, not empty, for a
metadata-less torrent (consistent with O2). And on a cold instance below 5.2.0 the N calls happen
before the first plan, which `strict_multi_client` (§3.5) then blocks planning behind — the first
run on a 5,000-torrent instance will feel it.

This is the design's single most important safety gate and it is not free. Saying so is the point.

### 3.4 Where false positives come from — ranked by damage

**FP-1 — Cross-seeds (highest likelihood × highest damage).** `cross-seed` and `autobrr` are
common in exactly this audience: one physical copy, N torrents, N trackers, sometimes across two
instances with different infohashes. A very common shape is Sonarr grabbing with category
`tv-sonarr` while the user's cross-seed of the identical content carries no category — a textbook
orphan by O1.
→ **Two defenses, not three, as of 2026-08-07.** The global path index (**O10**) and content
fingerprinting (§3.5). The third was hardlink-first placement, which made the whole class
survivable rather than fatal by costing zero bytes; under copy-only a missed cross-seed costs a
**full duplicate copy** of the payload. Both survivors are *detection*, not mitigation — so this
class is now caught or it is not, with nothing softening the landing. Said plainly because
under-stating a mitigation's disappearance is how a design keeps claiming a defense it no longer
has.

**FP-2 — `content_path == save_path`.** §3.2. Catastrophic and reachable from one naive line.

**FP-3 — An \*arr configured with a blank category.** Sonarr's `Category` field is *optional*;
leaving it blank produces only a recommendation warning, and when blank Sonarr polls
`torrents/info` **without** the category parameter — i.e. "any category". Sonarr and Orphanarr
would be watching the exact same torrents.
→ **`ignore_save_paths` globs do not mitigate this**, and it would be dishonest to claim they do:
a blank-category \*arr's downloads land in the *same* save path as the orphans, so no glob
separates them. What actually protects the user in v1 is already in the design and is worth naming
precisely — the loud first-run banner with the uncategorized count; `st_nlink > 1` as
confidence-reducing evidence (FP-8), which catches the already-imported case — and which is a
property of the *source*, so copy-only does not touch it; the settle window and stability gate; and
the re-check immediately before executing.

**The fifth item on that list is gone.** It read *"and hardlink-first placement, which makes racing
an \*arr survivable (worst case: one duplicate library entry, recoverable) rather than
destructive."* After 2026-08-07 the worst case is a duplicate library entry **plus a full second
copy of the payload**, so the sentence is false and the list is one item shorter. FP-3 is the worst
affected of the three analyses that cited hardlinking, because the paragraph directly above already
concedes that `ignore_save_paths` globs cannot separate a blank-category \*arr's downloads —
**leaving a loud banner and detection, with no mitigation at all.** Read-only \*arr queue exclusion
is a v1.1 proposal (Appendix B, D9, whose own ruling rests on the sentence just struck), and BRIEF
§5 **Q7 is now blocking-class** rather than merely open: it asks whether any \*arr is actually
running with a blank category, and the answer decides whether v1 needs that exclusion.

**FP-4 — `completion_on == -1`.** `Utils::DateTime::toSecsSinceEpoch()` returns `-1` for an
invalid `QDateTime`, and `m_completedTime` is only set when `completed_time > 0`. Torrents adopted
from a migrated client — *an orphan population the brief explicitly names* — have no completion
time. Any settling delay keyed on `completion_on` is broken for exactly the cases Orphanarr exists
to handle. → **O7 keys on `first_seen_at`.**

**FP-5 — Partially-selected torrents.** → O5.

**FP-6 — Path-mapping mismatch.** The benign shape is that nothing is found and the user concludes
the tool is broken. The malign shape is that a *different, unrelated* file exists at the mapped
path. → Mandatory per-client mappings; unmapped = error; wizard self-test that blocks; plan-time
assertion that every source exists and its size matches what the client reported.

**FP-7 — Torrents seeding from the library.** → O9.

**FP-8 — Already-imported content.** An \*arr's hardlink import leaves `st_nlink >= 2`. If its
category was later cleared, the leftover looks like an orphan but the media is already correctly
in the library. → `st_nlink > 1` is recorded as evidence and drops confidence below the auto
threshold. We can't cheaply say *where* without an inode scan; we can cheaply say *that*.

**FP-9 — Deliberate orphans.** Some users clear categories as a "hands off" marker. Indistinguishable
by definition. → Ignore tag settable from qBittorrent itself, plus a permanent per-torrent ignore.

**FP-10 — Non-media.** Linux ISOs, software, courses, personal files. → The classifier is
*required* to return `unknown`, and `unknown` means do nothing.

### 3.5 Multiple instances

- One row per instance: id, base URL, auth, TLS verify, timeout, poll interval, enabled, **its own
  path mappings**, **its own exclusion rules**.
- Torrent identity is `(client_id, infohash)`. Never infohash alone.
- **Content fingerprint** = `sha256(join("\0", sorted("<rel_path>:<size>")))` over selected files.
  This is the cross-instance and cross-seed identity key, and the key that sticky user decisions
  hang off, so a re-add under a new infohash inherits the user's choice. Torrent hash is not.
- One instance failing degrades to a Health warning and never stalls the others.
- **But plan creation is suspended while any enabled client is unreachable** (default
  `strict_multi_client: true`). If instance B is down we cannot know whether its torrents overlap
  instance A's candidate, and O10 is the gate that matters most. Refusing to act on incomplete
  information is the job. Escape hatch for genuinely isolated instances.
- The hardlink capability probe (§6.3) runs **per (client download root, library root) pair**, not
  once globally, and is rendered as a matrix.

### 3.6 Writes to the client: `addTags`, and nothing else

Both remaining "obvious" options have documented destructive side effects:

- **`setCategory`** returns 409 if the category doesn't exist, so it must be paired with
  `createCategory(name, savePath)`. Worse: `TorrentImpl::savePath()` returns
  `isAutoTMMEnabled() ? m_session->categorySavePath(category()) : m_savePath`, and the preference
  `torrent_changed_tmm_enabled` is documented as *"True if torrent should be relocated when its
  Category changes"*. **Setting a category can silently relocate the user's files** — under a
  seeding torrent, possibly across filesystems, while Orphanarr's freshly-created hardlinks still
  point at the old inodes.
- **`setLocation`** calls `torrent->setAutoTMMEnabled(false)` before setting the path. Filing a
  comic would silently change the user's torrent-management mode.

`POST /api/v2/torrents/addTags` carries no save-path semantics and is reversible by hand. The
local database remains the authoritative record; the tag is a convenience for the human looking at
qBittorrent's UI.

### 3.7 Authentication

**Preferred (≥5.2.0):** `Authorization: Bearer qbt_<...>`. The CSRF check is skipped for API-key
requests, so there is no `Referer`/`Origin` juggling and no session expiry. Gate on the **app**
version ≥5.2.0, or feature-probe — do not gate on "WebAPI ≥2.14.1", because no shipped release
carries a 2.14.x (5.1.0 is 2.11.4, 5.2.0 is 2.15.1), so that bound is never exercised at its
boundary. It is the exact idiom §3.8 tells implementers to avoid.

**Fallback (all versions):** `POST /api/v2/auth/login` with form `username`/`password`, capturing
the `SID` cookie.

**Send neither `Referer` nor `Origin` by default.** The widespread advice to inject them is
backwards. `WebApplication::isCrossSiteRequest()` returns *"not cross-site"* — allowed — when
**both** headers are absent, with this verbatim comment in the source:

```cpp
// owasp.org recommends to block this request, but doing so will inevitably lead
// Web API users to spoof headers so lets be permissive here
return false;
```

Identical in 4.6.7, 5.0.0, 5.1.0 and master. A bare Go client passes. **Injecting `Referer` moves
us out of that permissive branch into one that must same-origin-match `Host`** — or
`X-Forwarded-Host` when qBittorrent's reverse-proxy support is on — neither of which we control
behind an nginx `proxy_pass` that rewrites `Host` to an upstream address. The prescription creates
the failure it was written to avoid. Expose the headers as an opt-in per client for the unusual
deployment that needs them; do not send them by default.

**"Send neither" is not free in Go: `http.Client` sets `Referer` on redirect-followed requests.**
An http→https or trailing-slash 301 in front of qBittorrent is ordinary, and the retried request
then carries a `Referer` we never asked for, landing in the strict branch above. Set a
`CheckRedirect` that strips `Referer` — or refuses redirects outright on API calls, which is the
safer default since a redirecting qBittorrent endpoint is a misconfiguration worth surfacing.
"Redirect encountered" is its own Test-connection outcome, not a credentials failure.

Authenticate on **status code plus the presence of the `SID` cookie**, accepting an empty body.
The widespread idiom of checking for the body `"Ok."` is also wrong on 5.x:
`setStatus(APIStatus::Ok)` maps to `{.code = 200}` with no payload, and failure raises
`UnauthorizedHTTPError` → **401**, not 403.

**Status codes, and the diagnostic that has to work.** Rejection by credentials *and* rejection by
`validateHostHeader()` are both 401 with body `Unauthorized`, so the Test button cannot tell them
apart from the login response alone. It must **probe `GET /` first**: `200` on `/` plus `401` on
login ⇒ bad credentials; `401` on `/` ⇒ host-header rejection.

*Why that works — and the mechanism matters, because a plausible wrong one is one clause away.*
It is **not** because CSRF is skipped for the web root; the CSRF and host-header checks both run
before any path dispatch, so `/` and `auth/login` are gated identically. It works because **we
send neither header**, so `isCrossSiteRequest()` returns false on every path and host-header
validation is the only check that can fire. **Consequence: the diagnostic is only valid while
header injection is off.** If a user enables the opt-in, a `Host`-rewriting proxy makes CSRF fire
on both paths and produces the identical 401/401 signature — and the remediation text must then
*not* tell them to edit the Server-domains whitelist, because that is not the fix.

Host-header validation defaults **on**, and it **checks the port before the domain list**: a
client sending `Host: qbit.example.com:443` to a qBittorrent listening on 8080 is rejected with
the whitelist still at its `*` default. So the domain-list half only bites users who narrowed it,
but the port half bites exactly the reverse-proxy deployments this section exists to warn about.

**403 has two meanings, split by endpoint**, and the design depends on both: on `auth/login` it is
the `WebUIMaxAuthFailCount` **IP ban** (thrown only from `validateCredentials()`), which is why
the never-auto-retry rule below exists; on any non-login endpoint it is an **absent or expired
session**, which is why the re-login-once rule exists. Never collapse them.

**Never auto-retry credentials.** qBittorrent bans a client IP after repeated WebUI login
failures — this is the 403 case. On a poll loop that bans Orphanarr's container — and if Orphanarr and the user's browser
share an egress IP, **it locks the user out of their own qBittorrent**. On a login failure the
client goes to `auth_failed`, polling stops entirely, and a manual "Test connection" is required.
The correct backoff here is infinite until a human intervenes. Session expiry (a 403 from a
non-login endpoint) gets exactly one re-login and one retry.

### 3.8 State strings

qBittorrent 5.0 renamed `pausedUP`→`stoppedUP` and `pausedDL`→`stoppedDL`. **The published wiki was
never updated** and still documents `paused*` — and omits `forcedMetaDL` entirely. This broke real
consumers: Whisparr stopped importing completed downloads because it only recognized `pausedUP`.

`stoppedUP` and `queuedUP` remain **distinct** in every shipped release; a transient master-only
aliasing bug existed for ten days in 2024 and never shipped. O4 lists both, correctly.

**Branch on shipped `webapiVersion` values, not on invented ones.** Verified from
`src/webui/webapplication.h` per tag: 4.6.7 → `2.9.3`, 5.0.0 → `2.11.2`, 5.1.0 → `2.11.4`,
5.2.0 → `2.15.1`, master → `2.16.0`. The rename landed in the 5.0.0 line; **no shipped release
carries `2.11.0`**, so do not write a comparison against it.

Rules:
- The adapter normalizes raw strings to an internal enum. **Never compare a raw qBittorrent state
  string outside `internal/client/qbittorrent`.**
- Accept **both spellings**. Users run 4.x and 5.x side by side.
- An unrecognized state maps to `StateUnknown`, and the torrent is **skipped with a logged
  warning**. Fail closed. This is how the tool survives qBittorrent 6.0 without a release.
- Probe and store `app/version` and `app/webapiVersion` at connect. They are the single most
  useful numbers when diagnosing a shape mismatch later.

---

## 4. Media type detection

### 4.1 Principles

- **Content over name.** The torrent name is the weakest signal available. `Dune` is a movie, an
  audiobook and an ebook in three consecutive corpus entries, distinguished only by contents.
- **Bytes, not counts.** A 20 GB `.mkv` beside forty `.nfo`/`.jpg`/`.txt` files is a movie. A
  count-weighted census gets that spectacularly wrong.
- **Explainable or not acted on.** Every classification persists the ordered list of signals that
  produced it, with weights and details, and renders them verbatim in the UI. "94% of bytes are
  `.cbz`; `ComicInfo.xml` present; folder name matched no platform slug."
- **"I can't tell" is a first-class outcome** with a defined handler: leave the files alone, show
  the evidence, ask.

```go
type Classification struct {
    Type        MediaType   // movie|tv|music|audiobook|ebook|comic|rom
                            // |unknown|mixed|needs_extraction
    Cardinality Cardinality // single|multi|unknown   — orthogonal to Type, §4.4
    Confidence  float64     // classification score
    Signals     []Signal    // ordered; every signal that fired, with weight and detail
    Runners     []Candidate // the losing types and their scores
    Parsed      Parsed      // Title/Year/Series/Season/Episode/Volume/Platform/…
}

type Parsed struct {
    // …fields per media type…
    Confidence float64      // SEPARATE from Classification.Confidence.
                            // I9 gates on BOTH. §4.7 — "1917 (2019)" classifies as a movie
                            // with total confidence and parses with none.
}

func Classify(fs FileSet, rules Rules) Classification   // pure. no network, no clock, no I/O.
```

`FileSet` carries the probe fields (`Duration`, `Tags`, `ArchiveEntries`, `ContainerKind`) as
plain data. When they are unpopulated, `Classify` scores without them; `inspect/probe` fills them
and `Classify` runs again. See §2.3.

### 4.2 The signal pipeline

**S0 — Prune, then reject.**
Prune from consideration (not from the payload): `priority == 0`, `.!qB`, `*.nfo`, `*.txt`,
`*.sfv`, `*.srr`, images under `proof/`/`screens/`/`sample/`, and anything matching
`(?i)(^|[.\-_ ])sample([.\-_ ]|$)` that is <15% of the largest media file.

Then **reject outright** (→ `unknown` at confidence 0.95, meaning *we are confident this is not
media*): `.exe`, `.msi`, `.apk`, `.deb`, `.dmg`; a name matching
`(?i)\b(ubuntu|debian|fedora|arch|proxmox|truenas|windows\s*1[01]|macos)\b` combined with `.iso`.
Without this stage a Linux ISO is a textbook orphan that a naive classifier files as a game ROM.

**S1 — Extension census, byte-weighted.** Extension sets are sourced from the tools that already
own each domain, so we inherit their long tails: Sonarr's `MediaFileExtensions.cs` for video,
Lidarr's for audio, Readarr's for ebooks, Komga's library docs for comics, Audiobookshelf's
`globals.js` for its supported types.

Deliberate collisions, resolved by later stages rather than by picking a winner in the table:
`.iso` is video *and* ROM; `.pdf` is ebook *and* comic; `.zip`/`.rar` are archives *and* valid
Komga containers; `.md` is Sega Mega Drive *and* Markdown.

Near-decisive (weight ≥0.85): `.cbz .cbr .cb7 .cbt` → comic; `.m4b` → audiobook;
`.epub .azw3 .mobi .fb2` → ebook; the unambiguous ROM extensions.

**Per-library ingest rules, enforced by refusal — replacing the `.fb2` plan warning, 2026-08-07.**
Each library carries an `accepted_extensions` set, seeded from the target server's source with the
commit SHA it was read at, and `layout` **refuses** a file the configured server cannot ingest:
`SKIP_UNSUPPORTED_FORMAT`.

**Refuse, not warn**, on two independent grounds reached separately. A warning is not a gate, so a
warned item auto-files anyway and is invisible forever — which is exactly what the old `.fb2` rule
allowed. And I9 gates on confidence, cardinality, `mixed` and rootlessness, *not* on warnings, so a
warning here would have been the only one in the design expected to stop anything; §5.2 already
settled that shape when it routed date-based TV to review rather than warning about it.

The asymmetry is real and must be modelled rather than averaged: **`allow` for Komga, Audiobookshelf,
Navidrome, Plex and Jellyfin; `deny` for RomM**, whose scanner is an exclusion list (§6.5). Plex's
row is `[UNVERIFIED]` — it is the one server nobody on the team has read; seed it from Sonarr's
`MediaFileExtensions.cs` and say so, or tag it.

This subsumes the old rule and closes three holes at once:

- **`.fb2`** — Audiobookshelf's `SupportedEbookTypes` is `epub pdf mobi azw3 cbr cbz`; Readarr's is
  `.epub .kepub .mobi .azw3 .pdf`. Neither takes it.
- **`.mobi`, `.azw3`, `.kepub`** — BRIEF §5 A2 makes Komga the ebooks target, and Komga's reflowable
  support is EPUB-only, permanently. These now have no destination at all. → BRIEF Q26.
- **`.cb7` and `.cbt`** — a pre-existing hole nobody had noticed: both classify as comic at weight
  ≥0.85 above, and **Komga cannot open either.** (Jellyfin's bookshelf plugin documents both, which
  is precisely why the rule is per-library rather than global.)

Refused items get `SKIP_UNSUPPORTED_FORMAT`, **not `unknown`** — `unknown` would be false, and it
would make the review queue lie about why the item is there.

**S2 — Release-name grammar** (weight 0.35).
TV: `S\d{1,2}E\d{1,3}`, `\d{1,2}x\d{2}`, `S\d{1,2}E\d{1,3}-E?\d{1,3}`, `Season\s?\d{1,2}` with
≥2 video files, date-based `(19|20)\d{2}[-.]\d{2}[-.]\d{2}`.
Movie: a year token **and** exactly one video file ≥300 MiB **and** no episode marker.
Comic: `v\d{1,3}`, `vol\.?\s?\d{1,3}`, `#\d{1,4}`, `c\d{1,4}` with a comic extension.
ROM: `\((USA|Europe|Japan|World|...)\)` — the No-Intro region flag is a real standard
(*"the region flag is mandatory […] placed in parentheses"*), and RomM parses both `()` and `[]`
tags itself.
Audiobook: `(?i)\b(unabridged|abridged|audiobook|read by|narrated by)\b`, `\{[^}]+\}` (the ABS
narrator convention).
Music: `\[FLAC\]`, `\b(CD|Vinyl|SACD|EAC)\b`, `\b\d{3}kbps\b`, `\b(discography|EP|LP)\b`.

**S3 — Container/header probe** (weight 0.45). **A pure-Go tag and container-header reader. No
ffprobe.**

*When it runs.* Normally only when S1+S2 leave the result below the auto threshold, on at most 3
sampled files. **Two mandatory exceptions**, because a later section depends on the output rather
than on the score:

- **Any payload whose dominant class is audio** — music or audiobook — is always probed,
  regardless of confidence, over as many files as the decision needs (not a 3-file sample).
  §5.3 renders `{AlbumArtist}/{Album} ({Year})` from tags and `layout` is pure, so it cannot read
  them itself; establishing `COMPILATION`/`TCMP` for a Various Artists set needs more than three
  files; and §4.4's discography expansion needs per-album fields. Without this, a well-named
  `Pink Floyd - The Dark Side of the Moon (1973) [FLAC]/` clears 0.85 at S1+S2, is never probed,
  and §5.3 has no inputs — so every confidently-classified album would be treated as untagged and
  the music library would auto-file nothing.
- **Any `.cbz`/`.cbr`** gets its `ComicInfo.xml` read — a zip central-directory lookup, effectively
  free, and the fields feed §5.4 directly.
Duration comes from FLAC `STREAMINFO`, MP4 `mvhd`, and MP3 Xing/VBRI headers — milliseconds per
file, no external binary, no 150 MB dependency.

- *Music vs audiobook* — the one genuinely hard discrimination (§4.3).
- *`ComicInfo.xml` inside a `.cbz`/`.cbr`* → comic, decisive. A cheap zip central-directory read.
- *`.epub`* → parse `META-INF/container.xml` → OPF → `dc:title`, `dc:creator`. Ebook, plus author
  and title for free.
- *`.iso`* → peek the ISO9660 root: `VIDEO_TS/` ⇒ DVD; `BDMV/` ⇒ Blu-ray; `PS3_GAME/`/`SYSTEM.CNF`
  ⇒ console. Neither ⇒ `unknown`, never a guess.
- *Archive-only payload* → list entries without extracting. If they are comic images, `.zip`/`.rar`
  is a valid Komga book. Otherwise ⇒ `needs_extraction`, stop.

**S4 — Contextual hints** (weight 0.15, decisive only for ties, user-configurable). The leaf of
`save_path`, the tracker hostname, torrent tags.

**S5 — Structural shape** (tie-break only). ≥2 sibling `Season NN` directories ⇒ TV. Depth-2
`Artist/Album/*.flac` ⇒ music. Parent directory matching a RomM platform slug ⇒ ROM — this is what
rescues `.zip` and `.iso`. ≥20 sequentially-numbered images ⇒ loose-page comic.

**Tier 0 — deterministic overrides, short-circuit at confidence 1.00.** A prior manual decision
keyed on the content fingerprint; a user path rule (`/downloads/roms/**` → `rom`); a per-client
`default_media_type`.

> Path rules are the highest-value cheap feature in this design. Most people with ROM or comic
> orphans already save them to `/downloads/roms` and `/downloads/comics`, because that is how
> humans organize things. One config list turns an existing habit into a 100%-accurate classifier
> for about fifteen lines.

### 4.3 Music vs audiobook — stated honestly

Two folders of audio files. One is a 38-track audiobook; one is a one-track 61-minute album. The
corpus contains both (`folder_shapes.json#shp-009`/`#shp-010`) precisely because no extension or
structural signal separates them.

- `.m4b`/`.aax`/`.aa` present ⇒ audiobook, decisive, regardless of everything else.
- Name matches the audiobook grammar ⇒ audiobook, high.
- Otherwise, from S3 headers: `median_duration > 20 min AND track_count >= 5` ⇒ audiobook (0.80);
  `median_duration < 12 min` ⇒ music (0.80); **anything between ⇒ 0.50, below threshold, review.**
- Lossless present, or `.cue`/`.log`/`.accurip`, or ≥6 files with leading track numbers ⇒ music.

**There is no verified reliability figure for the duration heuristic and this document does not
invent one.** It is presented as a heuristic with a stated failure case, not as a rule. A silent
50/50 guess that files audiobooks into Navidrome is worse than an empty review queue.

### 4.4 Cardinality — one orphan is not always one item

Modelling classification as `FileSet → MediaType` cannot express *"this orphan contains many media
items"*, and a large fraction of real orphans do:

- `Pink Floyd - Discography (1967-2014) [FLAC]/` — 40 albums, not one
- `Nintendo 64 (USA) - No-Intro Set/` — 388 games, not one
- `Battlestar Galactica (2003) Complete Series + The Plan/` — a series *and* two films
- `Sandman Universe Week 2023-11-08/` — 8 unrelated comic series
- `Attack on Titan v01-v34/` — 34 books in **one** series; correct as-is

So `Cardinality ∈ {single, multi, unknown}`, orthogonal to `Type`.

**`multi` never auto-executes in v1.** It goes to review with a proposed *expansion* — here are
the 40 album folders I found, here is where each would go — and the user approves the batch or
edits it. This is cheap (it's a list) and it converts the worst category of silent misfiling into
a visible one.

The RomM case makes the cost concrete: RomM treats a folder inside a platform directory as **one
multi-file game**. Placing a No-Intro set at `/roms/n64/Nintendo 64 - No-Intro Set/*.z64` produces
*one* game with 388 files attached. The set must be **flattened** into `/roms/n64/*.z64` — a
388-file operation with 388 collision checks. That is exactly the operation a human should press a
button on. And `Final Fantasy VII (USA)/Disc1.bin,Disc2.bin,Disc3.bin` is the *opposite* case with
the same extensions: one game, correctly a folder. Cardinality is what tells them apart.

### 4.5 Scoring, thresholds, tie-breaking

```
score[T]   = Σ over evidence e supporting T of (e.Weight × e.ByteShare)
confidence = score[best] / max(1.0, score[best] + score[runnerUp])
```

| Band | Range | Behaviour |
|---|---|---|
| auto | ≥ 0.85 | eligible for automatic filing **if** `auto_file` is on and `cardinality == single` |
| review | 0.50 – 0.85 | plan created, **always** requires human approval |
| unclassified | < 0.50, or ambiguous, or `unknown`, or `mixed`, or `needs_extraction`, or parse confidence below threshold | no plan, no I/O |

**Fixed tie-break rules, applied after scoring, in order:**

1. `.m4b` present ⇒ audiobook.
2. Audio + ebook in one payload ⇒ audiobook (audiobook + companion PDF is a standard shipping form).
3. Comic extension + `.pdf` ⇒ comic.
4. Video + any other class ⇒ video family — **but only when no other class holds ≥30% of eligible
   bytes.** Without that qualifier this rule swallows every video-bearing payload and rule 8 below
   becomes unreachable, including for rule 8's own motivating example. `Show S01 + OST` is an
   ordinary shipping form and must not resolve to TV with the album silently stranded.
5. Within video: **any** episode pattern beats **any** movie pattern. A movie has zero episode
   markers, not fewer.
6. ROM + archive ⇒ ROM (ROM sets ship zipped by convention).
7. **`.bin`+`.cue` with a No-Intro/Redump region flag ⇒ ROM, never music.** Without this rule
   §4.3's *"`.cue`/`.log`/`.accurip` ⇒ music"* and §5.5's *"`.bin`+`.cue` is an ambiguous ROM
   container"* both fire on `Final Fantasy VII (USA)/Disc1.bin + Disc1.cue` with nothing to
   separate them. A `.cue` with no region flag and no platform hint stays ambiguous.
8. **No single type holds ≥70% of eligible bytes ⇒ `mixed`.** A payload containing a movie, an
   album and a book is not any one of them, and a byte-majority winner would file the movie and
   silently strand the other two. `mixed` is parked and surfaced exactly like `unknown`; splitting
   across libraries is a v2 feature with its own rollback story (§1.2).

   Rule 4 resolves *incidental* companions (a "making of" clip inside a game rip); rule 8 catches
   genuine multi-type payloads. **The two are separated by the 30% qualifier on rule 4, not by
   their ordering** — ordering alone made rule 8 dead code in the round-02 draft, since rule 4
   ran first and matched every video-bearing payload unconditionally.
9. Still within `ambiguity_margin` (default 0.10) ⇒ **ambiguous**.

Where those don't fire, ties break **by evidence tier, not by media type**: the type supported by
the strongest signal class wins, then the largest byte share, then `ambiguous`. A fixed type
priority list encodes "we always guess comic over ebook", which is wrong exactly as often as it's
right and is unexplainable to the user. Tier-based is explainable: *"we chose comic because the
`.cbz` extension is a stronger signal than the word 'Vol' in the name."*

**`.pdf` gets its own rule** because everyone gets it wrong. A PDF is a book, a comic, a scanned
magazine, a game manual, or a datasheet. There is no signal in the extension. A `.pdf` alone never
exceeds 0.50 and therefore always goes to review, unless it sits alongside `.cbz` (⇒ comic) or
`.epub` (⇒ ebook). Config `pdf_default: review | ebook | comic`, **default `review`**.

### 4.6 What happens when it can't tell

`unknown` is a **resting state, not an error**. The orphan is listed in the Review queue with the
top three candidates and scores, the full signal list with details, a file-tree preview, and
actions: assign type · assign destination · ignore once · **ignore forever**.

`ignore forever` writes a sticky decision keyed on the content fingerprint. That table is the
difference between a tool people keep running and a tool people uninstall.

A user correction writes a sticky decision but **v1 does not learn from it** — auto-generalizing
from one sample is how a misclick becomes a policy. Six corrections of the same shape is a signal
that a hint rule is missing, and the UI says so.

`tests/verification/corpus_lint.py` fails the build if fewer than 25% of corpus entries have a
negative expectation (currently 27%). **A classifier that never returns `unknown` cannot pass the
corpus.**

### 4.7 Known-hard cases, stated up front

- **Anime absolute numbering** (`[SubsPlease] Frieren - 12 (1080p) [A1B2C3D4].mkv`). Mapping
  absolute → season/episode requires an episode-count table, i.e. a metadata lookup. Plex's own TV
  naming article does not document an absolute-numbering scheme; Jellyfin's documents only
  `S##E##`. ⇒ **Always review**, with the absolute number pre-filled. Shipping a review queue
  entry beats shipping a guess that puts episode 12 of season 2 into `Season 01`.
- **Titles that are numbers or years**: `1917`, `2012`, `The 100`, `Ocean's 11`, `Se7en`,
  `Blade Runner 2049`. Each breaks a naive normalizer differently. The normalizer must **return
  low confidence rather than a guess** — and that confidence must be *gated on*, not merely
  recorded. **`Parsed` carries its own `Confidence`, separate from the classification score, and
  I9's auto band requires both to clear the threshold.** Without that clause,
  `1917.2019.1080p.BluRay.x264-GROUP.mkv` classifies as `movie` at high confidence (year token,
  one video ≥300 MiB, no episode marker), clears every gate, and auto-files under whichever token
  the year heuristic picked — the one remaining route in this design from a confident
  classification to a silently wrong result.
- **`Blade.Runner.2049.S01E01.mkv`** is TV. Rule 5 above.
- **Multi-platform ROM collections** ⇒ unknown platform ⇒ review with a platform picker.

---

## 5. Destination layout

One layout must satisfy **both** Plex and Jellyfin, because the stakeholder runs both. Where they
diverge, **v1 emits the intersection** and says so. Per-library `layout_profile` allows
`plex` / `jellyfin` / `plex+jellyfin` (default) / `passthrough`.

`internal/layout` is pure and I/O-free, which makes every rule below a table-driven test against
`tests/corpus/`. That is the only way these claims stay true as the servers move.

### 5.1 Movies — Plex + Jellyfin

```
{movies_root}/{Title} ({Year})/{Title} ({Year}).{ext}
```
```
/data/media/movies/Blade Runner (1982)/Blade Runner (1982).mkv
/data/media/movies/Blade Runner (1982)/Blade Runner (1982).en.srt
/data/media/movies/Arrival (2016)/Arrival (2016).mkv
/data/media/movies/Nosferatu/Nosferatu.mkv          ← year unparseable; degrades, doesn't fail
```

- **Folder per movie.** Plex recommends it; Jellyfin says movies *"should be organized into
  individual folders"* and the file *"should have the same name as the folder."* (The stricter
  *"must begin exactly with the parent folder name, character-for-character"* language governs
  **version grouping** only, not basic matching.) Both are satisfied either way.
- **Provider IDs: emit none.** Plex wants `{imdb-tt0372784}`; Jellyfin wants `[imdbid-tt0372784]`.
  These are not compatible in one folder name, and v1 does no lookup, so emitting one would be
  fabrication. Config `provider_id_style: none|plex|jellyfin` for later.
- **Editions: emit none by default.** Plex uses `{edition-Name}` (Plex Pass, PMS ≥1.28.1);
  Jellyfin uses ` - Label` with a required space-hyphen-space. Incompatible. Do not infer
  "Extended" from a scene tag — that is how you get two library entries for one film.
- **Multi-part movies go to review; v1 does not stack them.** The marker sets do intersect
  (`cd`, `dvd`, `disc`, `disk`, `part`, `pt`), but the *separator* does not: Plex documents
  `Title (Year) - ptN.ext`, and that space-hyphen-space string **is** Jellyfin's version-label
  syntax — so Jellyfin reads a two-part movie as two competing versions of one film. Jellyfin's own
  part form (`Movie Name-cd1.mkv`) has no spaces and is documented as *not* working alongside
  versions or merging. There is no verified form that satisfies both, so this design does not
  claim one. Also carried forward from Plex: parts must all be the **same container format**, and
  a stack caps at 8 — a mixed `.mkv`/`.avi` split set is a review item regardless.
- **No quality tag in the default filename.** Jellyfin's version grammar reads ` - Label` with
  optionally-bracketed labels, so emitting quality invites exactly the version-vs-separate-movie
  confusion above, and `Title (Year)` is what both servers match on regardless. Available as a
  config option, off by default.
  *(An earlier draft justified this with a Plex quote about bracketed text being ignored. That
  quote is real but comes from Plex's **TV** article, about the `Optional_Info` field, and its
  full sentence says such info is ignored by **legacy** agents while the current Plex TV Series
  agent **uses it as a matching hint** — the qualifier reverses the meaning. The movie article
  does not mention brackets at all. The Jellyfin argument carries the bullet on its own.)*
- **Sidecars:** `.srt`/`.sub`/`.idx`/`.ass` renamed to the video's stem with the language suffix
  preserved (`Movie (2019).en.srt`). `Sample/`, `Proof/`, `Screens/` dropped.
- **Extras folders are preserved, and the list is the union of both servers' vocabularies** —
  `Behind The Scenes`, `Deleted Scenes`, `Featurettes`, `Interviews`, `Scenes`, `Shorts`,
  `Trailers`, `Other`, `Clips`, `Extras`, `Samples`, `Theme-Music`, `Backdrops`. Plex's own list
  notably **does not** include `Extras` (its catch-all is `Other`); Jellyfin's does. Taking one
  server's list silently mishandles the other's folders, and deleting any of them destroys content
  the user wanted.
- **Never rename a file whose stem ends in an inline-extra marker**, and — by the same argument as
  the folder list above — **the marker set is the union of both servers**, not Plex's alone.
  Plex's 8 are `-behindthescenes`, `-deleted`, `-featurette`, `-interview`, `-scene`, `-short`,
  `-trailer`, `-other`, and Plex requires the filename to end in that value *exactly* ("the hyphen
  is important"). Jellyfin adds `-clip`, `-deletedscene`, `-extra`, `-sample`, **and `.`, `_` and
  space variants of `trailer` and `sample`** — so the rule cannot be expressed as "stem ends in
  one of these hyphenated strings." Model it as a set of **(separator, token) pairs** over
  separators `-`, `.`, `_`, ` `.

  Without this, the sidecar rule above renames `Making of-clip.mkv` or `Preview.trailer.mkv` to
  the video's stem and promotes an extra into a second main movie — the exact bug this bullet
  exists to prevent, for the other server, two bullets after the document argues that taking one
  server's list silently mishandles the other's.

### 5.2 TV — Plex + Jellyfin

```
{tv_root}/{Series} ({Year})/Season {NN}/{Series} ({Year}) - s{NN}e{MM}.{ext}
```
```
/data/media/tv/Doctor Who (2005)/Season 01/Doctor Who (2005) - s01e01.mkv
/data/media/tv/Doctor Who (2005)/Season 00/Doctor Who (2005) - s00e11.mkv
/data/media/tv/Chernobyl (2019)/Season 01/Chernobyl (2019) - s01e01-e02.mkv
```

- **`Season NN`, zero-padded, the English word.** Plex: *"Be sure to use the English word 'Season'
  when creating season directories, even if your content is in another language."* Jellyfin: *"Do
  not abbreviate to `S01` or `SE01`"*, *"Pad with leading zeros."* Both agree.
- **Specials ⇒ `Season 00`.** Plex accepts *either* `Season 00` or a `Specials/` folder; Jellyfin
  documents only `Season 00`. Intersection wins. This is exactly where "roughly like Plex" gives a
  Jellyfin user a phantom show called "Specials".
- **Multi-episode ⇒ `s02e18-e19`.** Plex and Jellyfin document the same shape, case aside.
  Jellyfin's docs show uppercase and Plex's canonical example is lowercase; both parsers are
  believed case-insensitive, `[UNVERIFIED]` — we emit lowercase to match Plex's example.
- **Date-based shows** (talk shows, news, dailies) go to **review**, with the date pre-filled and
  Plex's documented filename form `{Series} ({Year}) - {YYYY-MM-DD}.{ext}` pre-rendered.

  They cannot be auto-filed, and the reason is structural rather than cautious: Plex's own worked
  example is `/The Colbert Report (2005)/**Season 08**/The Colbert Report (2005) - 2011-11-15…`.
  That is a **real season number, and nothing in the date yields it** — deriving it needs the
  episode table §1.2 forbids. This is the same missing-table problem as anime absolute numbering,
  and §4.7 routes that to review; the two cases now agree. Emitting a filename we cannot place is
  worse than a review entry, and a plan *warning* would not have helped: **I9 gates on confidence,
  cardinality, `mixed` and rootlessness — not on warnings.**

  (Jellyfin documents no date-based scheme, though its parser does carry date expressions. A regex
  existing is not a scanner outcome, so this stays `[UNVERIFIED]` and does not affect the ruling.)
- **Episode titles omitted.** Both formats mark them optional and getting them needs a lookup.
- **Series year emitted only when parsed.** Both tolerate its absence; inventing one is worse.
- **Merging into an existing series folder is the normal case.** Match case-insensitively and
  **reuse the existing spelling** — otherwise a case-sensitive filesystem gets `severance/` next to
  `Severance/`, and a case-insensitive one gets a silent merge.
- **Season packs** produce one step per episode file in one plan, approved and rolled back
  together. A file whose episode number cannot be parsed is listed as `unplaced` and requires
  explicit acknowledgement. **We never silently drop a file from a plan.**

> **Normalization is load-bearing for TV in a way it is not for movies.** Dropping a raw release
> folder into `/TV` gives Plex a *series* named `Show.S03E05.1080p.WEB.h264-GRP`. That is a broken
> library, not an untidy one. A movie folder named `Some.Movie.2009.1080p.BluRay-GRP` mostly still
> matches on title+year. If layout effort is ever cut, cut it from movies and music — not TV,
> comics, or audiobooks.

Plex also warns that mixing movie and TV content under one path *"will likely result in incorrect
matching […] and can also result in some files being completely ignored."* Orphanarr therefore
refuses to configure the movie and TV roots as the same directory, or one as an ancestor of the
other.

### 5.3 Music — Navidrome

**Navidrome organizes *"entirely based on the metadata tags in your audio files"* and *"does not
use folder names or file names to group tracks."*** This changes the design rather than decorating
it.

```
{music_root}/{AlbumArtist}/{Album} ({Year})/<original filenames, unchanged>
```
```
/data/media/music/Boards of Canada/Music Has the Right to Children (1998)/
    01 - Wildlife Analysis.flac
    cover.jpg
    CD2/...
```

1. **Never rename tracks.** Renaming cannot improve Navidrome's discovery, and it can break `.cue`
   sheets, `.log` files, `.m3u` playlists, gapless references, Beets, Plex music, and the seeding
   torrent's file list. The only reason \*arrs rename is to satisfy their own file-tracking model,
   which Orphanarr does not have.
2. **Never write tags.** Forbidden by **I1**, not merely deferred.
3. **`AlbumArtist`/`Album`/`Year` come from the tags, not the release name.** The tag read is not
   free — §4.2 S3 pays for it unconditionally on every audio payload, over as many files as the
   decision needs, precisely so this section has inputs. Deriving them from the release name
   guarantees the folder name
   and Navidrome's own grouping disagree, at exactly the moment the user is debugging.
   `COMPILATION`/`TCMP` ⇒ `Various Artists`.
4. **Untagged albums go to review**, or to a configurable `{music_root}/_untagged/`. A tagless
   album is invisible to Navidrome wherever you put it; filing it silently just hides the problem.
5. `cover.jpg`/`folder.jpg` and `CDn/` subfolders are carried through untouched — the only
   path-derived things Navidrome cares about.

### 5.4 Comics — Komga

**Corrected from source 2026-08-07 — the previous statement of this rule came from Komga's docs
and the docs are wrong.** `FileSystemScanner.kt`, `postVisitDirectory` L156-158, keyed on
`file.parent` at L119-121:

```kotlin
val books = pathToBooks[dir]        // keyed on the IMMEDIATE parent
val tempSeries = pathToSeries[dir]
if (!books.isNullOrEmpty() && tempSeries !== null) { … scannedSeries[series] = books … }
```

| The docs say | The source does |
|---|---|
| A Series per subfolder, whatever the depth | A Series **only** for a directory *directly containing* ≥1 scanned file. Directories holding only subdirectories emit **nothing**. |
| Series are hierarchical | `scannedSeries` is **flat**. |
| The name derives from the path | `dir.name` — the **last component only** (L101). |
| Root-level files are ignored | `walkFileTree` visits `root`, so root-level books form a Series **named after the library root**. |

The folder **is** the series name — still a structural fact, not a convention. But it is the
*immediate* folder, and only when that folder actually holds books. The practical consequence is in
§5.7: a `{Author}/{Title}/` tree emits one series per *title* and nothing at all for the author.

```
{comics_root}/{Series}/<book files>
```
```
/data/media/comics/Saga/Saga v01 (2012).cbz
/data/media/comics/Attack on Titan/Attack on Titan v01.cbz
/data/media/comics/_oneshots/Watchmen (1986).cbz
```

- Series name from the parse, stripped of trailing volume-range/year/tag runs
  (`Saga v01-v09 (2012-2019) (Digital)` → `Saga`). Book filenames preserved unless a volume/issue
  number was parsed confidently, in which case zero-pad — so `v01`…`v41`, never `v1`…`v41`.
  **The reason previously given here was wrong.** `SeriesLifecycle.kt:45` uses
  `CaseInsensitiveSimpleNaturalComparator.getInstance()`, applied at `:78` over `book.name`
  (`path.nameWithoutExtension`), then `number = index + 1` at `:88` — **Komga's comparator is
  natural, so `v2` already precedes `v10` unpadded.** Pad anyway: it helps every non-natural-sorting
  consumer, and OPDS clients are not all Komga. Keep the rule, drop the false justification.
- **One-shots** go to Komga's configured one-shots directory, matched against *any part* of the
  path. Only routed there if the user has told us the directory name; otherwise its own series
  folder.
- **Refuse a one-shots directory name that occurs anywhere in the library root's own absolute
  path.** The match is `dir.pathString.contains(oneshotsDir, true)` (`FileSystemScanner.kt` L159) —
  a **case-insensitive raw substring on the absolute path** — so a one-shots name of `comics` under
  `/data/media/comics` turns the entire library into one-shots. Applies identically to §5.7's
  ebooks library. `oneshotsDirectory` defaults to `null` (`Library.kt:37`), so "the user has told
  us" must mean *confirmed set*, not *assumed*.
- **`scanCbx` / `scanPdf` / `scanEpub` are per-library toggles, all defaulting true**
  (`FileSystemScanner.kt` L57-62). A file filed into a library whose toggle is off is **invisible**,
  not merely mis-shelved. BRIEF §5 Q34.
- Formats: `cbz`, `zip`, `cbr`, `rar`, `pdf`, `epub` — with a condition that must not be dropped:
  **CBR support excludes solid archives, and RAR5 works on macOS/Windows and Docker amd64+arm64
  but not Docker `arm` or the bare jar.** A `.cbr` filed onto a Docker-`arm` Komga is a book Komga
  cannot open, so `.cbr` carries a plan note.
- **Do not convert `cbr`→`cbz`.** On a **link**, converting rewrites a hardlinked, seeding file
  (I1). On a **copy** the destination is a fresh inode and I1 does not apply — so state the reason
  per branch rather than asserting one mechanism for both. The ruling is unchanged on scope: §1.2
  forbids rewriting archives, and a conversion we perform is a conversion we have to be right about.
- **The converse is worth knowing, because it is the one place a media server reaches into our
  files.** Komga's own `convertToCbz` and `repairExtensions` (both default **off**) *delete*
  and *rename* files inside the library — and **where the placement was a hardlink** those files
  are the seeding torrent's inode. The torrent survives (`#C3`/`#C4`: unlinking or renaming one name leaves the
  other valid), and §6.7's Undo dev/ino check correctly refuses afterwards rather than deleting
  something unexpected. So Orphanarr fails closed here, but a user who enables those Komga options
  should know Undo will stop working for those books.
- **A torrent containing multiple different series must not be flattened into one folder.** Komga
  would present it as one 8-book series and untangling it means a re-scan. → `cardinality=multi`,
  one series folder per detected title, human confirms.
- **Loose page images** are a comic Komga cannot read. Confidence capped, routed to review with the
  message *"Komga requires an archive; these are loose images."* No CBZ packer in v1.
- Komga requires that no two libraries share any part of their path. Orphanarr validates root
  nesting.

### 5.5 Game ROMs — RomM

Structure A: `library/roms/{platform}/` and `library/bios/{platform}/`.

```
/data/media/roms/roms/snes/Chrono Trigger (USA).sfc
/data/media/roms/roms/gba/Golden Sun (USA, Europe).gba
/data/media/roms/roms/psx/Final Fantasy VII (USA)/Final Fantasy VII (USA) (Disc 1).cue
```

- **Preserve the original filename. Always.** RomM parses `()` and `[]` tags for region, language
  and revision, and those tags are searchable in its UI. A No-Intro or Redump name is already
  canonical. Renaming destroys exactly the metadata RomM wants. **This is the one media type where
  doing nothing to the filename is the correct engineering.**
- **Platform folder = a RomM slug.** RomM detects platform from the *folder*, not the file — its
  scanner has no extension→platform map — so Orphanarr owns that table.
- **Do not hardcode slugs, and do not trust either docs page.** For PlayStation 1 there are
  **three** different published answers and **neither documented one is usable**: the
  folder-structure example tree shows `ps/`, the Supported Platforms table shows `ps1`, and the
  source enum says `psx`. The enum wins, because `scan_handler.py` uses the folder name directly
  as `fs_slug`/`slug` and matches it against the enum-keyed platform list. It is a `StrEnum`, so
  **the folder must equal the enum's string *value*, not its member name** — `psx`, never `PSX`;
  `UPS("psx")` resolves and `UPS("PSX")` raises. Worth spelling out in the one section whose
  entire point is that people transcribe the wrong token. Verified values: `PSX = "psx"`,
  `NDS = "nds"`, `DC = "dc"`, `NGC = "ngc"`, `GENESIS = "genesis"`, `SNES = "snes"`,
  `SMS = "sms"`. The enum grows per release — 454 at 4.5.0, 458 at 4.8.1/4.9.0, 460 at master —
  which is itself the argument for the next sentence. Ship it as seed data **vendored with the
  commit SHA it came from**, render it as an editable mapping in Settings, and let the user
  override —
  which is conceptually what RomM's own `system.platforms` remap does. An unknown platform is
  `unknown`, never an invented slug: a wrong slug creates a folder RomM ignores and the user has to
  find and undo it.
- **Ambiguous containers** (`.iso`, `.bin`+`.cue`, `.chd`, `.cso`, `.pbp`, `.zip`) span
  psx/ps2/psp/saturn/dc/ngc/wii *and* real PC software. → review with a platform picker, unless S3
  header sniffing or an S4 hint resolves it. Guessing files a Saturn game into `psx/` and RomM's
  match then fails silently.
- **Multi-disc / multi-file games are a folder** — RomM's documented shape, with `dlc`, `hack`,
  `manual`, `mod`, `patch`, `update`, `demo`, `translation`, `prototype` subfolders left alone if
  present. v1 does not *construct* that taxonomy from a torrent's layout.
- **BIOS files always go to review.** RomM's BIOS folder is *"entirely optional"*, BIOS files are
  identified by hash rather than filename in practice, and a known-filename table across ~460
  platforms will never be complete. Routing everything BIOS-shaped to review with a platform
  picker costs nothing and avoids a permanently half-maintained table. Wrong BIOS placement
  silently breaks emulation.
- **ROM sets flatten; single games don't.** §4.4.

### 5.6 Audiobooks — Audiobookshelf

ABS's folder grammar *is* its metadata parser, so our job is to not destroy information rather
than to reproduce it. Patterns `{Author}/{Series}/{Book}` or `{Author}/{Book}`; *"single file books
can be in the root folder […] otherwise every book must be in its own folder"*; series sequence
followed by `" - "` or `". "` or preceded by `Vol`/`Vol.`/`Volume`/`Book`; publish year first or
directly after the sequence; narrator in `{braces}`; ASIN in `[brackets]`; disc subfolders
`Disc`/`CD`/`Disk` + number.

```
{audiobooks_root}/{Author}/{Series}/{Seq} - {Year} - {Title} {{Narrator}}/<original files>
{audiobooks_root}/{Author}/{Title}/<original files>
```
```
/data/media/audiobooks/Terry Goodkind/Sword of Truth/1 - 1994 - Wizards First Rule {Sam Tsoutsouvas}/Disc 1/01.mp3
/data/media/audiobooks/Andy Weir/Project Hail Mary/Project Hail Mary.m4b
```

Audio filenames preserved (ABS reads embedded metadata and sorts by filename); only the folder path
is authored. When the author cannot be extracted, `Unknown Author/{original name}/` — ABS still
ingests it and the user fixes metadata in ABS, which is a better tool for that job than Orphanarr
will ever be. **Honest degradation beats clever guessing**, and that is a general rule in this
design, not a local one.

### 5.7 Ebooks — Komga. Enabled by default as of 2026-08-07

**Calibre is off the table for direct writes.** Its manual: *"the contents of this folder are
automatically managed by calibre, **do not** add any files/folders manually to this folder, as they
may be automatically deleted."* Calibre-Web reads that same Calibre-managed library and builds its
book list from `metadata.db`, where the `id` in `Author/Title (id)/` is a database primary key.
Dropping an `.epub` into the folder does not add the book, and hand-writing an id produces a
disk/DB mismatch that Calibre-Web won't show.

**BRIEF §5 A2 answers Q2: Komga, including PDFs.** So ebooks and comics are served by the *same*
program from two library roots — and Komga forbids libraries sharing any part of their path.

**The previously-specified tree was Kavita/ABS-shaped and is replaced.** From
`FileSystemScanner.kt` (`postVisitDirectory` L156-158, keyed on `file.parent` at L119-121):
**Komga creates a Series only for a directory that *directly contains* at least one book file.**
Directories holding only subdirectories emit nothing, `scannedSeries` is flat rather than
hierarchical, and the series name is `dir.name` — the last path component only (L101). So
`{Author}/{Title}/{Title}.epub` gives a user with 400 ebooks **400 single-book series with the
author invisible**. `EpubMetadataProvider` reads `dc:creator`, `dc:title`, `dc:date` and
`belongs-to-collection`/`group-position` from the OPF, with `importEpubBook`/`importEpubSeries`
defaulting true (`Library.kt:17-18`) — **so an `{Author}/` level buys nothing: it emits no Series
and the author is recovered from the file anyway.**

The emitted tree is §5.4's comics rule at a different root, in three branches:

```
1. series parses          {ebooks_root}/{Series}/{Series} {NN} - {Title}.{ext}
2. no series, oneshots    {ebooks_root}/{oneshots_dir}/{Author} - {Title}.{ext}
   dir CONFIRMED set
3. otherwise              {ebooks_root}/{Title}/{Title}.{ext}
```
```
/data/media/ebooks/Discworld/Discworld 03 - Equal Rites.epub
/data/media/ebooks/Project Hail Mary/Project Hail Mary.epub
```

**Branch 3 is not optional and the Komga default is why.** `oneshotsDirectory` defaults to `null`
(`Library.kt:37`) and the one-shots path only fires `if (!oneshotsDir.isNullOrBlank() && …)`. With
it unset, branch 2 would produce **one Series named `_oneshots` holding every standalone ebook the
user owns** — worse than the outcome this section exists to prevent.

**Refuse a one-shots directory name that occurs anywhere in the library root's own absolute path.**
The match is `dir.pathString.contains(oneshotsDir, true)` (L159) — a case-insensitive raw substring
on the *absolute* path — so an `oneshotsDirectory` of `books` under `/data/media/ebooks` turns the
entire library into one-shots. **This applies identically to §5.4's comics library.**

Zero-pad the sequence — but **not** for the reason §5.4 gives. `SeriesLifecycle.kt:45` uses
`CaseInsensitiveSimpleNaturalComparator.getInstance()`, applied at `:78` over `book.name`, so
**Komga sorts naturally and `v2` already precedes `v10` without padding.** Pad anyway: it helps
every non-natural-sorting consumer, and OPDS clients are not all Komga.

**MOBI, AZW3 and `.kepub` have no destination** — Komga will never support them, and neither will
any other server in the target set. They are refused by the per-library ingest rule (§4.2) with
`SKIP_UNSUPPORTED_FORMAT`, **not** classified `unknown`, which would be false and would make the
review queue lie about the reason. BRIEF §5 Q26 asks whether a second ebook library is wanted
instead.

**Recorded dissent (D18):** the Fact Checker graded the *old* tree `[VERIFIED — safe]` for
standalone books and `[PARTIAL]` only for multi-volume works — the `{Author}` directory contains no
book files, so it produces no series and no empty-series pollution; the level is inert rather than
harmful. **Nothing was invisible under the old tree.** This section is therefore series-hygiene and
idiom, not a data-loss fix.

### 5.8 Cross-cutting sanitization

Not optional, and easy to get wrong:

- `/` and NUL are the only characters illegal on POSIX. `: " < > | ? * \`, reserved stems
  (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`9`, `LPT1`–`9`), and trailing dots/spaces are illegal on
  SMB/NTFS/exFAT. **A Linux-only test suite will not catch these** (proven: `#C14`) — so the rule
  is per-destination, flagged during setup, with `/proc/mounts` auto-detection pre-filling the
  answer and the user's answer winning.
- **255 *bytes* per path component, not characters.** 132 non-ASCII characters can exceed
  `NAME_MAX` (proven: `#C13`). Truncate the *title* portion on a UTF-8 rune boundary and record it
  as a plan warning. **Budget 233 bytes, not 255, always** — §6.5 appends the 22-byte
  `.orphanarr-partial.tmp` suffix in the destination directory, so a name truncated to exactly 255
  fails `ENAMETOOLONG` on the copy path: the long non-ASCII title this rule exists to support.
  **The budget is unconditional, not "whenever the operation is a copy."** `layout` is a pure
  function (§2.3) that runs *before* the executor attempts anything, so it cannot know the method;
  and after the 2026-08-07 amendment copy is the primary path anyway. Derive the number as
  `255 − len(suffix)` rather than hard-coding it — `#C30` pinned the previous 18/237 pair and had
  to be re-pinned when the suffix changed. **The extension and the structural marker are both protected** — the episode
  code (`- s22e01`), the volume/issue number, and the disc number are what the scanner matches on,
  and truncating a long Cyrillic series title from the left would otherwise remove the marker and
  leave a file no server can place.
- Unicode → **NFC**. Preserve everything else. **Do not ASCII-fold.** People have Japanese,
  Cyrillic and accented libraries; mangling them is an insult, not a feature.
- **Case-insensitive destinations.** Probe once per library by creating `.orphanarr-case-probe` and
  stat'ing `.ORPHANARR-CASE-PROBE`. Collision detection is case-insensitive where the probe says
  it must be.
- Any sanitization that changes a name is recorded as a plan warning, visible before execution.

---

## 6. File operation semantics

Every rule here exists because the alternative loses data.

### 6.1 The prime directive

> **Orphanarr never modifies, moves, renames, deletes, chmods, or chowns a source file. Ever. No
> config key enables it in v1.**

This is not timidity; it buys four concrete properties:

1. **The worst failure class becomes structurally impossible.** "Orphanarr broke my seeding
   torrent" and "Orphanarr lost my files mid-copy" cannot happen. On a private tracker a broken
   seed is a hit-and-run and potentially an account — the user's *reputation*, not their disk.
2. **Undo is trivial and complete.** Every operation is an *addition*. Undo is removing what we
   added. Rollback is provably total because we only ever added.
3. **It is what the ecosystem already does.** Sonarr and Radarr import by hardlink and leave the
   torrent seeding.
4. **The cost is bounded and visible.** After the 2026-08-07 amendment the normal case is **one
   full copy** — zero bytes only on the pairs where the §6.3 probe has upgraded to hardlinking.
   Orphanarr says which, in the plan, in gigabytes, before it happens.

The objection is that this doesn't fully solve *"orphans pile up in the completed directory
forever."* Correct — and under copy-only it is sharper than "correct." **Nothing in this design
ever returns disk space**, so the lifetime ceiling on what Orphanarr can file is `free − reserve`,
once. The brief's stated harm is *"Nobody imports them,"* not "my disk is full," and cleanup is a
separate feature with a separate risk profile that belongs to the stakeholder to request — but
BRIEF §5 **Q25** now asks it as a blocking-class question rather than a nicety, because on a full
array the answer decides whether v1 terminates at all. See §0 finding 3. (Post-file source cleanup
is **Q25**; the earlier cross-reference to Q4 was stale.)

### 6.2 Placement

**Inverted 2026-08-07 (BRIEF §5 A1).** Copy is the primary path; hardlinking is an optimisation
that must be earned per pair.

| Situation | Operation |
|---|---|
| Default | **verified copy** (§6.5) |
| The §6.3 probe has passed for *this* (download root, library root) pair | **`os.Link`** — one syscall, O(1), zero bytes, torrent never notices |
| `link` returns `EXDEV`/`EPERM`/`EMLINK`/`ENOSYS`/`EOPNOTSUPP` | **verified copy**, and the fallback and its errno are recorded in the ledger |
| Destination configured `force_copy` | copy |

**The upgrade is per-pair, not global, and it is surfaced rather than silent.** §3.5 and §6.3
already model the probe per (download root, library root); an all-or-nothing switch would force
copies onto provably-linkable pairs, spending the one resource §0 finding 3 establishes is finite
and non-renewable, for no safety. And because an automatic capability upgrade is a configuration
change the user did not make, it is shown, not just applied. *(Dissent D16: two agents held that
`OPS__MODE` should keep defaulting to `link_or_copy` rather than `copy`. The per-pair upgrade moots
most of that disagreement, which is why it is the adopted form rather than either side's.)*

The UI reports the outcome as *"copied 43 — 1.2 TB used. 0 linked: separate mounts. A single
`-v /pool:/data` would have used 0 bytes"*, not as an undifferentiated success. **The
counterfactual is required, not decorative**: under copy-only the user has no other way to learn
that their mount topology, not their hardware, is what is consuming a terabyte.

**§6.5's publish step is still a hardlink, on 100% of placements, and nothing in this inversion
changes that.** `link(partial, dst)` runs in the destination directory — one directory, one
vfsmount — so it is unaffected by mount topology, by `:ro`, and by this table. It is the only
operation here that is **safe by default**: `link(2)` returns `EEXIST` rather than overwriting,
where `rename(2)` destroys an existing destination silently and with no error (proven: `#C9` —
`b'IRREPLACEABLE'` became `b'replacement'`). **I2 is mechanically true because of that step and
nothing else.** Three agents blocked the round-01 vote on it; a reader who concludes from this
section that "we don't hardlink any more" and simplifies §6.5 back to `rename` re-ships the hazard.

Directories cannot be hardlinked (`EPERM`, `#C6`), so the executor recreates directories and links
each file individually. **It iterates the manifest from `torrents/files`, never a `WalkDir` of the
source tree** — the same rule as §3.2 and for the same reasons, which apply with more force at
execution time: a tree walk picks up `.!qB` incomplete markers and, for partially-selected
torrents, everything qBittorrent parks under `.unwanted/` (`UNWANTED_FOLDER_NAME`), which is
precisely the population O5 exists to exclude. **Symlinks encountered while resolving a manifest
entry are refused, not followed** — a symlink to `/` would turn the operation into an attempt to
link the root filesystem.

### 6.3 The `st_dev` trap

The folklore is *"hardlinks work if the files are on the same filesystem."* That is not the
kernel's test. From `fs/namei.c`, `filename_linkat()`:

```c
error = -EXDEV;
if (old_path.mnt != new_path.mnt)
        goto out_dput;
```

It compares **vfsmount pointers**, not superblocks. Reproduced without Docker and without root in
`tests/verification/bindmount_hardlink_test.sh`:

```
LAYOUT A — the common Docker mistake: -v /hostdata/torrents:/downloads -v /hostdata/media:/media
  st_dev(downloads)=47  st_dev(media)=47
  [VERIFIED] st_dev is IDENTICAL across the two bind mounts
  [VERIFIED] link() across them FAILS with EXDEV despite equal st_dev

LAYOUT B — the TRaSH/Servarr-recommended single mount: -v /hostdata:/data
  [VERIFIED] link() SUCCEEDS; st_nlink=3
```

Consequences:

1. **Differing `st_dev` rules hardlinking out; equal `st_dev` does not rule it in.** `st_dev` is
   used for the UI device map and capacity planning — **never as the gate.**
2. **The only sound probe is a real `link(2)`**, run once per (download root, library root) pair at
   config time on a real file, with the result cached and shown as a badge:
   *hardlink available* / *copy only — separate mounts* / ***copy only — source is on a read-only
   mount***.
   The third outcome is not cosmetic. Without it the remediation banner tells a user running `:ro`
   download mounts to do something they have already done, because `:ro` is itself a separate
   vfsmount and returns `EXDEV` even when everything is on one filesystem behind one `-v`
   (verified: `#R2`, and the trap reproduced directly).
3. **The remediation is a mount-topology fix, not a filesystem fix**, and the UI must say so:
   one `-v /data:/data`, not separate `-v` for downloads and media — **and not `:ro` on the
   download side**, which forecloses hardlinking permanently. See §11.1: the two postures are
   mutually exclusive and the user picks one. BRIEF §5 Q32.

Other cases where `link` fails or `st_dev` misleads: ZFS datasets and btrfs subvolumes on one pool
are separate filesystems; mergerfs with a path-preserving create policy returns `EXDEV`; CIFS/SMB
often has no hardlinks (`[UNVERIFIED]` and probably too strong as a blanket claim — the Linux cifs
client does implement `->link` for SMB2+ against servers that support it); overlayfs upper layers
have copy-up semantics (Orphanarr refuses to place a library root there and says why); `EMLINK` at
ext4's 65 000 link limit is rare but real for heavily cross-seeded files.
**Attempt the link and handle the error. Never predict.** — which is why the uncertainty in that
list costs nothing.

**unRAID's mover is a fourth-quadrant case worth naming**, because unRAID is explicitly a target
platform (§2.1) and the failure is invisible: the mover **can silently break hardlinks** when it
migrates a file between cache and array, so the user's free space quietly halves weeks after
Orphanarr ran, and the inode Orphanarr recorded no longer matches. See §6.7 for what that does to
Undo. `[PARTIAL — widely and repeatedly reported on the unRAID forums, but conditional: the
reported mechanism is the allocation method placing inode-sharing files on different array disks,
not an unconditional property of the mover. Not verified by this team.]` The design consequence
does not depend on the cause — §6.7 must handle a changed `(dev, ino)` however it happened.

### 6.4 Hardlink consequences that must be stated

- **A hardlink is not a snapshot** (`#C5`). Writing through one name is visible through the other.
  → I1 forbids tag writing, archive rewriting, and in-place mutation **on a link**.
- **Permissions live on the inode** (`#C19`). `chmod` on the library copy also chmods the download
  copy. **A copy is a fresh inode and does not** (`#C21`, the exact converse). → I12.
- **Ownership is inherited on a link, and this was the #1 silent failure.** qBittorrent running as
  uid 1001 with umask 077 writes `-rw------- 1001:1001`. Plex runs as 1000. The hardlink succeeds.
  Orphanarr logs success — because it *was* a success. Plex sees nothing. The user sees *"it said
  it worked and my library is empty,"* with no error to search for.

**The 2026-08-07 amendment largely dissolves that failure rather than merely mitigating it.** Under
copy we stop inheriting qBittorrent's ownership at all: the destination is a fresh inode owned by
Orphanarr, with a mode we control. What replaces the old rule:

- **Modes are set on `{dst}.orphanarr-partial.tmp`, after the copy, before publish — never on a
  published path.** The hardlink path has no partial, so it **structurally cannot reach a chmod**.
  Freshness is proven by construction rather than by trusting a recorded field, which matters
  because §3.5's per-pair matrix means one library can hold both linked and copied files. `#C26`
  proves the mode survives the `link`+`unlink` publish.
- **Mode comes from configured `file_mode`/`dir_mode`, never from the source.**
  `os.Chmod(dst, srcInfo.Mode())` is one well-intentioned line away from reproducing the very bug
  this fixes. `#C24` shows umask silently strips bits, which is why §8.2 carries these keys at all.
- **Directories are `mkdir`ed and then `Chmod`ed**, because `#C24` proves `mkdir`'s mode argument
  is masked by umask and cannot deliver `dir_mode`. Permission to do so is gated on
  `plan_step.created_dirs_json` — *did we create it* — a better discriminator than link count.
- **No `chown`.** It needs `CAP_CHOWN` (`#C23`), which contradicts D3's first-class
  `--user 1000:1000`, and it cannot fix a group mismatch anyway. The portable levers are mode,
  shared group, and umask. **Setgid library roots (`#C25`) are an operator instruction, not an
  Orphanarr operation** — I12 correctly forbids us performing it.

**Preflight, split by path:**

- **Copy path:** check the *destination* ownership, computable from config alone. The original
  rule — stat the source, ask whether `media_server_uid` can read it — checks the wrong inode here,
  because the destination is a fresh inode with Orphanarr's uid/gid and umask. It fires falsely on
  every `0600` source and stays silent on the real failure (`UMASK=077` in our own container).
- **Link path: keep the original source-stat preflight, unchanged.** On a linked placement the
  destination *is* the source inode, so the config-only check computes the wrong file. This matters
  precisely because §6.2's remediation banner invites the user to consolidate mounts: they take the
  advice, the probe passes, the pair upgrades, no mode is set (there is no partial), and the
  destination inherits `0600 uid=1001`. **The reward for following our own advice must not be the
  failure this bullet exists to prevent.** O8 already stats, so retaining it costs no syscalls.
- **Source readability is a blocking preflight on both paths** (`SRC_UNREADABLE`). `open(src,
  O_RDONLY)` is required for a copy — and the link is *not* a fallback: `safe_hardlink_source()`
  requires `MAY_READ|MAY_WRITE` and systemd ships `fs.protected_hardlinks = 1`, so an unreadable
  source fails both ways. BRIEF §5 Q33.

### 6.5 Copy

Every copy:

1. Writes to `{dst}.orphanarr-partial.tmp`, **in the final directory** — so the publish step is
   same-filesystem by construction — opened **`O_CREAT|O_EXCL`**. `#C34`: a partial reused without
   `O_TRUNC` leaves a stale tail that passes a size check.
2. Verifies size.
2b. **Re-`stat`s the source and asserts `src_size`/`src_mtime`/`src_dev`/`src_ino` unchanged.**
   Fails with `SRC_CHANGED` if not. See I13 below — this is the step whose absence produces a file
   that is wrong in a way nothing downstream can detect.
3. `fsync`s the file, then the directory. **This is the step that gets omitted**, and without it a
   power loss leaves a correctly-named, correctly-sized, silently zero-filled file that Plex will
   scan and the user will find six months later.
3b. **`fchmod`s the partial** to the configured `file_mode` (§6.4). On the partial, never on a
   published path; `#C26` proves the mode survives publish.
4. **Publishes without clobbering** — see below.
5. Unlinks the partial on **any** exit that is not a successful publish — including a collision
   routed to `skip`, which is not a "failure" but leaves the same debris. `Reconcile()` would
   sweep it at next start, but until then a full-size copy of a 60 GB remux sits in a library root,
   in a design that makes a point of free-space preflight. The destination is never partially
   populated.

**Step 4 must not be `rename(2)`, and this is not a detail.** `rename` destroys an existing
destination silently and with no error (`#C9`), which is the one thing I2 says cannot happen. The
collision check in §6.6 runs at *plan* time, and plans sit in the review queue awaiting approval
for hours or days by design — so the check-to-publish window is the length of the review queue,
not microseconds. Anything that creates the destination in that window (the user; a Sonarr or
Radarr import into a shared library, which BRIEF §5 Q8 flags as an open coexistence question)
would be destroyed, with no error and no journal row. Worse, rollback would then remove
Orphanarr's copy too, leaving the user with **neither** file and falsifying §6.1's claim that
rollback is total because we only ever added. The hardlink path has no such gap only because
`link(2)` happens to return `EEXIST`.

So the publish is, in order:

1. **`link(partial, dst)` then `unlink(partial)`.** Same directory, therefore same filesystem,
   therefore always available wherever `rename` was — and `EEXIST` routes to the §6.6 collision
   policy instead of destroying anything.
2. Where the destination filesystem has no hardlinks (exFAT, some SMB servers):
   **`renameat2(..., RENAME_NOREPLACE)`**.
3. Only if neither is available (`EINVAL`/`ENOSYS`): re-`stat`, apply the collision policy, then
   plain `rename` — and **record the residual race as a plan warning** rather than leaving it
   implied.

This makes I2 mechanically true rather than aspirational, which is the standard §10.1 sets for
itself. `#C9` already supplies the fixture for the required test: create the destination between
the copy and the publish, assert the pre-existing bytes survive and the plan reports a collision.

**The marker's final extension must be one the target server will not ingest, and this is checked
per library at config time.** `.orphanarr-partial` — the original name — has no leading dot and is
not hidden, because the name exists to be greppable and swept by `Reconcile()`. That was safe while
every target server used an **allowlist** of extensions. **RomM does not.** Its
`exclude_single_files()` (`base_handler.py:270`) is an *exclusion* list — everything not matching
`["db","ini","tmp","bak","lock","log","cache","crdownload"]` (`config_manager.py:34-43`) plus seven
filenames (`:44-52`) is kept, tested as `file_name_lower.endswith("." + ext)` (`:280`), with no
dot-file rule. RomM therefore **indexes the partial and hashes its truncated bytes** for
hash-database matching, and after publish holds a row for a vanished path *plus* a duplicate for
the real ROM. Those defaults seed two fields — `EXCLUDED_SINGLE_EXT` (`:374-386`) and
`EXCLUDED_MULTI_PARTS_EXT` (`:407-419`), the latter read by `roms_handler.py:343-345` before it
walks and hashes a multi-part ROM directory, which is where a 388-file ROM set actually lands.

Under hardlink-first this was near-theoretical. Under copy it exists for the full duration of every
placement. **Hence `.orphanarr-partial.tmp`** — `tmp` is already in RomM's exclusion list, and the
four allowlist servers (Komga, Jellyfin, Navidrome, Audiobookshelf) never saw it either way. Plex
is `[UNVERIFIED]` (BRIEF §5 Q34 territory; nobody has read its scanner).

*A hidden staging **directory** does not work:* Komga skips dot-directories, but RomM would read
`/roms/snes/.orphanarr-staging/` as a multi-file game.

An interrupted cross-filesystem copy otherwise leaves a plausible-looking partial at the
destination (proven: `#C17` — 4 KiB of 1 MiB) that a scanner will happily index.

**Verification defaults to size + successful `fsync` + the step-2b source re-check.** Checksumming
a 60 GB remux costs minutes of I/O for a failure mode Go's stdlib does not exhibit. `verify:
checksum` (BLAKE3) is opt-in, and it applies to copies only — checksumming a hardlink is theatre,
since source and destination are one inode. *(Dissent: D5, re-voted 3–2 against this ruling on
2026-08-07 and upheld under PROCESS §3.4; recorded as D17.)*

> **I13 — a copy must prove its source did not move under it.** `#C29`: a source mutated during the
> copy yields a destination matching **neither** the old nor the new contents, at exactly the right
> size, after a successful `fsync`. Size and `fsync` cannot detect it and **a checksum does not
> either** — the hash matches what we read. The two answer different questions and neither
> substitutes for the other. This class did not exist under hardlinking, because `link(2)` never
> opened the source; a copy holds a live file in a running client's storage open for minutes to
> hours, and §3.1's stability gate ran at scan time, possibly days earlier. The fix is two `stat`
> calls against `plan_step` columns that already exist. BRIEF §5 Q38 asks what else prunes those
> directories — it decides whether this is a safety net or a routine event.

**Free space — four rules, all of which were invisible while `copy_bytes` was usually zero.**

1. **Account per destination *filesystem*, not per root path**, identified by `st_dev` of the
   library root. Seven library roots on one pool each `Statfs` their own root, each see the same
   free space, and each independently approve. *This is the legitimate use of `st_dev` §6.3 blesses
   — accounting, never linkability.* Stated limitation: btrfs subvolumes and ZFS datasets report
   distinct `st_dev` while sharing a pool, so the aggregate can overcommit. **Do not build a
   pool-topology detector; `ENOSPC` remains authoritative.**
2. **Re-check in the executor**, against a fresh `Statfs`, before each plan and at each step
   boundary — not only at plan time. Plans sit in review for days *by design*; twelve
   individually-passing plans aggregate into `ENOSPC` at plan eleven. §6.6 already established
   exactly this shape for the *destination*; space was left behind. Failure is `blocked`, not
   `failed`. Maintain a **committed-bytes ledger** over approved-but-unexecuted plans so approval
   is honest.
3. **`reserve = max(absolute, fraction × total)`**, not a flat 1 GiB — which is noise on 40 TB and
   means "fill until 1 GiB remains." The harm is not Orphanarr stopping; it is Orphanarr filling
   the array so **qBittorrent, Plex and the next download** fail, with no visible connection to us.
   Too large is a visible complaint; too small is a 3 a.m. outage. BRIEF §5 Q30.
4. **Use `Bavail`, not `f_bfree`, and budget `st_size`.** `#C28` measured **11.61 GiB** of
   root-reserved ext4 space that `f_bfree` reports and an unprivileged process can never have;
   `#C31` shows sparse files inflate a naive size sum.

Enforce the reserve against **`/config`'s filesystem too** where it is shared: a full array means
SQLite cannot extend its WAL, so the journal that records what we were doing fails at exactly the
moment it is needed. `DISK_SPACE_BLOCKED` (§10.5) is the event code. Refuse the **whole plan**,
with the numbers. A tool that fills a user's array at 3 a.m. and then can't write its own database
is a tool that gets uninstalled.

*Considered and rejected: a cumulative `MAX_COPY_BYTES_PER_RUN`.* Two agents proposed it
independently and a third withdrew it by applying his own test — with a correct reserve and a fresh
per-plan `Statfs`, the array cannot be filled past the reserve, so a byte budget makes the fill more
gradual rather than safer. It prevents nothing.

**No reflink/`FICLONE` fast path in v1** — and the honest reason is a judgement call, not a
verdict. `#C15` shows `cp --reflink=always` is unsupported on ext4/tmpfs, but *unavailability* is
an argument for attempt-and-fall-back, which is exactly what §6.2 does for `link(2)`. The actual
reason is `[UNVERIFIED]` caution about silent partial-copy behaviour on exotic filesystems, and a
simplicity ruling under PROCESS §3.4. It is a cheap win on btrfs/XFS/ZFS setups and a reasonable
early v1.1 addition; it is being skipped deliberately, not because the evidence forbids it.

### 6.6 Collisions

**The destination is evaluated twice: at plan time, and again by the executor immediately before
each step.** The draft-time check is what the user reviews; the execute-time check is what keeps
I2 true across the review-queue window. The design already re-reads the *torrent* before executing
(§3.1, §10.2); it must re-read the *destination* for the same reason.

Before flagging, two cheap checks resolve most "conflicts" automatically:

| Destination state | Action |
|---|---|
| Doesn't exist | proceed |
| Exists, same `(dev, ino)` as source — **link path only** | **already linked in.** No-op, mark done. Cannot fire on a copy, where the destination is a fresh inode by definition. |
| Exists, and a `plan_step` row with `created_by_us` claims it with matching `src_size`/digest | no-op, mark done. **This is what makes re-runs free and retries safe under copy.** |
| Exists, same size, **no journal row** | **collision — apply policy.** Never a silent no-op. |
| Exists, different content | policy |
| Exists as a directory where a file is planned, or vice versa | **fail the job, always.** Never resolve automatically. |

**Idempotency moved from the filesystem to the journal on 2026-08-07, and the replacement rule
matters more than the row it replaced.** The obvious substitute — "same size and matching
fingerprint, so no-op" — is undefined and unsafe: `content_fingerprint` is torrent-level, per-file
digests are off by default, and a size match would bless a file the *user* put there, which Undo
would then delete. **A same-sized file with no journal row is a collision, always.** Add the
one-`SELECT` plan-time `already_filed` lookup as well; without it a re-added, already-filed torrent
generates a plan whose every step skips and which then marks itself `partial`.

Policy: `skip` (default) · `suffix` (` (2)`) · `fail`.

**There was a fourth, `keep_larger`, and it was removed.** There is no non-destructive
implementation of it: the branch where the incoming file is larger must delete or replace the file
already at the destination, which I2 forbids and which the paragraph below forbids again. Size is
also simply the wrong discriminator — a well-encoded x265 loses to a bloated x264 rip. It reached
the draft as a merge artifact from two proposals while three others explicitly ruled it out, and
it is logged as **D13** rather than quietly deleted.

**There is no `overwrite` policy in v1. Not as a default, not as an option.** Overwriting is the
only way this program can destroy a file the user already had, so the capability does not exist.
Every "replace if better quality" heuristic ever written has eventually eaten someone's remux and
replaced it with a 700 MB rip. A user who wants replacement can delete the file themselves — a
thirty-second manual action versus a whole class of unrecoverable automated mistakes.

A `skip` marks the *plan* `partial`, which is a visible, actionable state — **not** a success.

### 6.7 Journal, crash recovery, rollback

**A multi-file plan is not atomic and this design does not pretend otherwise.** There is no POSIX
primitive that renames 340 files as a unit. What is guaranteed is that **every intermediate state
is recorded and recoverable.**

Each step row is written `in_progress` **before** the syscall and updated after, with
`src_dev`/`src_ino`/`src_size`, `dst_path`, and `created_by_us` — the flag rollback keys on.

**On startup, `Reconcile()` runs before any new work:**

```
for each step in state in_progress:
    if dst exists and verifies against recorded size (and digest, if recorded)  -> done
    elif dst exists but does not verify   -> delete dst (we created it), -> pending
    else                                  -> pending
    if the step cannot be verified either way -> roll back and surface it. Never silently resume.

    ALWAYS, after the branch resolves:
        delete any matching .orphanarr-partial.tmp CLAIMED BY A plan_step ROW
                                                        <-- unconditional, not an elif
                                                        <-- but journal-scoped: see below
for each plan executing with no in_progress steps:
    -> resume from the first pending step
```

**The partial sweep is unconditional and that is not a detail.** §6.5 publishes with
`link` + `unlink`, so there is a window in which *both* names exist. A crash inside that window
leaves a `dst` that verifies — first branch, `done` — with the partial still on disk. As an `elif`
the sweep would never run, leaving a stray name in a directory a media server scans, and
falsifying §6.5's statement that the partial exists to be swept. It costs no bytes (same inode)
and no data, but the claim would be false. *Found independently by two agents at the round-02
vote, which is usually a sign the shape of the bug is more general than the instance.*

**And it must be journal-scoped, which it was not.** As originally written the sweep unlinked *any*
path matching the marker, whether or not a `plan_step` row claimed it — **the only unlink in this
design that is not journal-scoped**, and therefore the only one that could delete something we did
not create. Low probability, pre-existing, and the identical rule §6.6 now applies to collisions:
*our output is what the journal says it is, never what the filesystem's shape suggests.* The sweep
became load-bearing rather than cosmetic when copy became the only path, which is what surfaced it.

This is roughly 150 lines. It is the difference between *"the container was OOM-killed 40 minutes
into a 200 GB copy and resumed cleanly"* and *"the user has an unknown number of half-written files
and a Discord thread."*

**Rollback deletes only paths recorded with `created_by_us = true`**, in reverse order, then removes
directories this job created **if and only if they are empty**. It never touches a path it did not
record creating. Because sources are never deleted (§6.1), a completed rollback returns the
filesystem to its exact pre-plan state.

**Auto-rollback is off for clean in-run failures.** If a plan fails at step 200 of 340 with
`ENOSPC`, we do *not* delete the 199 files just written — the user may prefer to free space and
resume. The plan sits in `failed` with three buttons: Resume · Roll back · Ignore. *(Dissent: D6.)*
An **unclean** exit is different: anything unverifiable at `Reconcile()` is rolled back and
surfaced, never resumed.

**Undo** from History: for a hardlink, confirm `(dev, ino)` still matches what was recorded, then
`Remove(dst)`. For a copy, confirm size and mtime. Undo is disabled with a stated reason where it
cannot be proven safe.

**When `(dev, ino)` no longer matches, do not tell the user their file was tampered with.** On
unRAID that message would be false for every file the mover touched (§6.3), and unRAID is a named
target platform. Fall back to matching on path + size + the recorded plan, and offer Undo behind
an explicit confirmation that states what could not be verified. Failing closed is right; failing
closed with a wrong explanation is not.

**A flat journal that survives the database.** Every completed operation is *also* appended as one
JSON line to `/config/journal/YYYY-MM.ndjson`:

```json
{"ts":"2026-08-06T21:44:02Z","op":412,"method":"hardlink","src":"/data/torrents/…/The.Matrix.1999.mkv","dest":"/data/media/movies/The Matrix (1999)/The Matrix (1999).mkv","bytes":68719476736}
```

SQLite corruption, a botched migration, a `docker run` with the wrong `-v`, or a user deleting
`/config` all destroy the DB. The journal is a flat text file the user can `grep` from a rescue
shell to answer the only question that matters at that moment — *where did my files go?* It costs
one append per operation. It is redundant with the database, and that is the point.

### 6.8 Dry-run

`dry_run: true` is the shipped default and stays on until the user turns it off deliberately, with
a confirmation restating the mode and the byte totals. A persistent banner shows while it is on.

Dry-run produces the *exact* `Plan` a real run would: every `(src, dst, mode, bytes, src_dev,
dst_dev, collision, warning)` tuple, link bytes vs copy bytes, free space before and after.
Rendered in the UI and downloadable as `plan.json`. Because the plan is persisted, *"why did it do
that"* is answerable after the fact without reproducing.

---

## 7. Data model

SQLite (`modernc.org/sqlite`), `/config/orphanarr.db`, `journal_mode=WAL`, `foreign_keys=ON`,
`busy_timeout=5000`, numbered `.sql` migrations embedded and applied forward-only in a transaction.
No ORM, no migration framework, no down-migrations.

Why a database rather than JSON files: crash-safe writes mid-plan, a queryable action history, and
unique constraints that actually enforce identity. Why not Postgres: one container, one user,
thousands of rows.

```sql
client(id, name, kind, base_url, username, password_enc, api_key_enc, verify_tls,
       timeout_s, poll_interval_s, enabled, state, app_version, api_version,
       last_seen_at, last_error, created_at)
    -- state: ok | unreachable | auth_failed | unknown

path_mapping(id, client_id, remote_prefix, local_prefix)        -- longest prefix wins

library(id, media_type, name, root_path, layout_profile, enabled, options_json)
    -- options_json: edition_style, provider_id_style, oneshot_dir, romm_structure,
    --   platform_slug_overrides, untagged_fallback, collision_policy, force_copy,
    --   non_posix, case_insensitive, media_server_uid, media_server_gid

torrent(client_id, infohash, infohash_v2, name, category, tags, state, tracker_host,
        progress, amount_left, size, total_size, save_path, content_path,
        added_on, completion_on, has_metadata,
        content_fingerprint, first_seen_at, last_seen_at, stable_since,
        PRIMARY KEY (client_id, infohash))
    -- first_seen_at is OUR clock. The settle window uses THIS, never completion_on. §3.4 FP-4.

torrent_file(client_id, infohash, idx, rel_path, size, priority, resolved_local_path)
    -- the authoritative manifest. Never a WalkDir. §3.2

classification(id, client_id, infohash, media_type, cardinality, confidence, ambiguous,
               parse_confidence,                   -- SEPARATE from confidence; I9 gates on BOTH
               signals_json, runners_json, parsed_json, classifier_version,
               source, created_at)                 -- source: auto | user

plan(id, client_id, infohash, library_id, classification_id, state, mode, dry_run,
     link_bytes, copy_bytes, warnings_json, gate_result, gate_reasons_json,
     created_at, approved_at, approved_by, started_at, finished_at, error)
    -- state: planned | awaiting_review | approved | executing | committed
    --      | partial | failed | rolled_back | rejected | ignored

plan_step(id, plan_id, seq, op, src_path, dst_path, bytes,
          src_dev, src_ino, src_size, src_mtime, dst_dev, dst_ino,
          method, method_actual, fallback_errno, created_by_us, created_dirs_json,
          state, digest, started_at, finished_at, error, UNIQUE(plan_id, seq))
    -- op: mkdir | hardlink | copy | noop
    -- created_by_us is THE rollback-eligibility flag.

decision(id, content_fingerprint, kind, value, note, created_at)
    -- kind: ignore | force_type | force_library | force_platform | force_destination

rule(id, kind, pattern, media_type, enabled, hits, created_at)   -- Tier 0 path globs

event(id, ts, level, code, client_id, infohash, plan_id, message, data_json)

setting(key, value_json, source, updated_at)      -- source: db | env (env rows read-only in UI)
```

Three points worth defending:

1. **`plan` → `plan_step` is one-to-many, deliberately.** One torrent can produce thirteen
   destinations. A schema with one destination per torrent cannot represent a season pack, and that
   is not a corner case.
2. **`content_fingerprint` is the cross-seed and cross-instance identity key**, and the key sticky
   `decision` rows hang off — so a re-add under a new infohash inherits the user's choice. Torrent
   hash is not stable enough for this.
3. **Timestamps are Unix seconds, integers, UTC.** Not strings, not local time. Every timezone bug
   in this class of tool comes from the other choices.

Secrets are AES-GCM encrypted with a key derived from a `0600` file in `/config`. This is
obfuscation at rest, not real secrecy — anyone with the config volume has both — and **the UI says
so plainly** rather than implying more.

Retention: `event` pruned to 30 days; `plan`/`plan_step` retained indefinitely (they are small and
they are the undo record); `torrent` rows not seen for 30 days are deleted so a removed torrent
doesn't haunt the overlap index forever.

**No table on the undo path carries a cascading FK to `torrent`.** With `foreign_keys=ON` and a
naive `REFERENCES torrent ON DELETE CASCADE`, the 30-day torrent prune would take the entire undo
history with it — 30 days after a user removes a torrent from qBittorrent, which is precisely the
moment they are most likely to come looking for *"where did my files go."* That means `plan`,
`plan_step` **and `classification`**: stating it for `plan` alone reopens the hole transitively,
since `plan.classification_id` references a table keyed on `(client_id, infohash)`. Each
denormalizes the identifiers and the torrent name it needs.

---

## 8. Configuration & first run

### 8.1 One source of truth per setting

Two sources of truth is a bug. The rule:

- **Configuration lives in SQLite and is edited through the UI.** The brief mandates a web UI for
  configuration; if a file were authoritative, the UI would be either decorative or a fight over
  who wrote last. This is also the \*arr convention.
- **Every setting is overridable by an environment variable at boot.** Env-sourced settings render
  **read-only in the UI with an "(set by environment)" badge**. Compose users are not stranded.
- `GET /api/v1/config/export` renders the current config as YAML with secrets redacted, for backup
  and diffing.
- Env prefix `ORPHANARR__`, double underscore for nesting — the `Sonarr__` convention this audience
  already knows.

### 8.2 Keys that carry a decision

```
ORPHANARR__OPS__DRY_RUN            default true       ← ships ON
ORPHANARR__OPS__AUTO_FILE          default false      ← ships OFF
ORPHANARR__OPS__MODE               link | link_or_copy | copy      default copy
                                   ← auto-upgrades to link_or_copy PER PAIR as §6.3's probe
                                     passes, and the upgrade is surfaced, not silent. (D15, D16)
ORPHANARR__OPS__COLLISION          skip | suffix | fail            default skip
ORPHANARR__OPS__CLIENT_WRITE       none | tag                      default tag    (§3.6, BRIEF Q11)
ORPHANARR__OPS__VERIFY_COPIES      default false      (size + fsync + I13 source re-check always)
ORPHANARR__OPS__MAX_PLANS_PER_RUN  default 25         ← a misconfiguration hurts 25 items, not 25 000
                                     Note it is byte-blind: 25 plans can be 25 GB or 25 TB.
                                     §6.5's per-plan Statfs is what actually bounds the damage.
ORPHANARR__OPS__RESERVE_BYTES      default 10 GiB     ← floor; effective reserve is
ORPHANARR__OPS__RESERVE_FRACTION   default 0.05         max(RESERVE_BYTES, FRACTION × total)
ORPHANARR__OPS__FILE_MODE          default 0644       ← §6.4. Applied to the partial, pre-publish.
ORPHANARR__OPS__DIR_MODE           default 0755         mkdir's mode is masked by umask (#C24),
                                                        so directories are mkdir'ed then chmod'ed.
ORPHANARR__SCAN__INTERVAL_SECONDS  default 900
ORPHANARR__SCAN__SETTLE_SECONDS    default 300        (from first_seen_at)
ORPHANARR__SCAN__STRICT_MULTI_CLIENT  default true    §3.5
ORPHANARR__DETECT__AUTO_THRESHOLD     default 0.85
ORPHANARR__DETECT__REVIEW_THRESHOLD   default 0.50
ORPHANARR__DETECT__AMBIGUITY_MARGIN   default 0.10
ORPHANARR__DETECT__PDF_DEFAULT        review | ebook | comic   default review
PUID, PGID, UMASK, TZ
```

Plus, per client: `path_mappings`, `ignore_tags`, `ignore_save_paths` (globs), `ignore_trackers`,
`default_media_type`. Per library: root, profile, and the `options_json` fields in §7.

**Startup validation is reported, never fatal.** A container that exits on a config error is a
container whose UI the user cannot open to *fix* the config error. Start, serve the UI, show the
problems in red, do no work.

### 8.3 First run — the 40 TB problem

The single most likely way this tool ruins someone's day is being pointed at an eight-year-old
qBittorrent with 1,400 uncategorized torrents and doing something to all of them.

Five steps, in this order, because each gates the next:

1. **Environment.** Effective UID/GID/umask, `/config` writable, API key generated and shown once.
   Admin credentials set — **no default credentials, ever, and no "no authentication" option that
   isn't a deliberate choice.**
2. **Add a client.** Test reports `app/version`, `webapiVersion`, whether Bearer auth is available,
   the torrent count, **the uncategorized count**, and a sample of `save_path` values. A failure
   distinguishes bad credentials from CSRF/Host-header rejection. *That uncategorized count is the
   whole pitch, delivered in step two.*
3. **Path mapping.** For each distinct observed `save_path`, apply the mapping, `stat()` it, show
   ✅/❌ with the failing path quoted verbatim. **You cannot enable a client whose mappings do not
   resolve.** This step is the difference between a tool that works and a two-year GitHub issue
   titled "does nothing."
4. **Libraries.** One root per media type. On each pick, immediately show: writability, free space,
   the case-sensitivity probe, the non-POSIX question, and **the device map with a real `link(2)`
   probe** — `Hardlinks: YES`, `Hardlinks: NO — separate mounts; will COPY`, or `Hardlinks: NO —
   source is on a read-only mount` (§6.3's third badge outcome; without it the remediation tells a
   topology-B user to do what they have already done). Validates root nesting (movies ⊄ tv, Komga's
   no-shared-path rule, no library root inside a download path).
5. **Baseline scan, dry-run. The summary is a bill, not a statistic.** *"Found 1,412 uncategorized
   completed torrents, 38.4 TB. Classified: 601 movies, 340 TV, 88 music, 12 comics, 4 ROM sets,
   367 unclassified. **Filing these would copy 38.4 TB. `/data/media` has 4.1 TB free with a 210 GB
   reserve — you can file roughly 340 of them now.**"* **Zero files touched.**

   The arithmetic costs a division and it is the difference between *"it stopped and told me why"*
   and *"it stops after about 300 files."* Under copy-only every filed orphan permanently consumes
   its own size and nothing ever gives space back (§0 finding 3), so the ceiling is a fact about the
   user's array that they should learn in the wizard rather than four hours into a run.
   `DISK_SPACE_BLOCKED` is a first-class skip-reason row on §9's dashboard, with the byte shortfall.

**Step 3 offers the identity mapping first.** Under BRIEF §5 A3 the container mounts the clients'
download folders, and the common case is mounting them at the path the client already reports —
one `stat` confirms it and the step disappears.

A user who clicks Next on everything ends up in a state where **Orphanarr cannot touch a file**
until they deliberately approve one.

`initial_scan: new_only | all` (default `new_only`) exists, but it is not the only protection — a
user who picks `all` still gets dry-run.

---

## 9. Web UI

A Preact SPA over the JSON API, built in CI and embedded via `//go:embed`. No component library, no
state-management library, one stylesheet, dark by default, left nav — the \*arr idiom the brief
asks for. **Everything the UI does goes through the same public REST API at `/api/v1/`**,
authenticated by `X-Api-Key` (the scheme Sonarr publishes in its OpenAPI document) or a session
cookie. Matching that means existing \*arr-adjacent tooling already knows how to talk to us.
*(Dissent: D2.)*

**Six screens. No more.**

1. **Orphans** — the working list. Client · name · size · type + confidence badge · cardinality ·
   proposed destination · state · warnings. Row expands to the **signal trail ("why?")**, the file
   manifest with priorities, and the exact per-file plan. Bulk select with a filter
   (`type:movie confidence:>0.9`) — the 1,412-orphan user needs this on day one, and it is a
   checkbox column, not a feature.
2. **Review** — everything below the auto threshold, plus ambiguous, plus `cardinality=multi`, plus
   unknown ROM platform, plus rootless torrents, plus collisions. Actions: confirm type · choose
   library · pick platform · edit destination · ignore once · **ignore forever**. Every action
   writes a sticky decision.
3. **History** — committed plans with method, bytes, timestamps, the op journal, and an **Undo**
   button that actually works, with honest labels where undo is unavailable. This is the *"what did
   you do to my files"* screen and it must be answerable without opening a log.
4. **Settings** — Clients (+ Test), Path Mappings (+ live resolution), Libraries (+ the capability
   matrix), Detection thresholds and hint rules, File Operations, General. **The probes are
   re-runnable, not wizard-only** — configuration drifts.
5. **System** — versions, uptime, UID/GID/umask; **Storage**: the device map + hardlink matrix +
   free space per root; Health warnings (persistent, actionable, dismissible-with-reason); Logs;
   Events. Storage is the single most useful screen in the app and nobody in this ecosystem does it
   well.
6. **Dashboard** — counts by state, per-client status pills, and **a skip-reason breakdown**:
   `147 skipped: path not found` · `23 skipped: overlaps another torrent` · `9 skipped: settle
   window`. Repeating this because it is the highest-value observability feature in the design: the
   most common real-world outcome is *"Orphanarr isn't doing anything,"* and the reason is always
   in that panel.

**Not in v1:** charts, statistics, calendar, multi-user, SSO, notification-provider matrix, i18n,
theme switching, mobile layouts, a file browser.

**The API, since §9 claims the UI adds nothing the API cannot do.** JSON, `/api/v1/`, `X-Api-Key`
or session cookie, SSE for live updates (one-directional; a WebSocket is not needed and is
mangled by more reverse proxies).

```
GET    /health                                 liveness, no auth
GET    /system/status                          versions, client reachability, device map,
                                               hardlink matrix, free space, dry_run flag
GET    /clients          POST /clients         POST /clients/{id}/test
POST   /clients/{id}/probe-paths               the §8.3 step-3 mapping self-test
GET    /libraries        PUT  /libraries/{id}  POST /libraries/{id}/probe
POST   /scan                                   202 + scan id
GET    /orphans?state=&type=&client=&confidence=&q=
GET    /orphans/{id}                           evidence + manifest + plan
GET    /orphans/{id}/explain                   the full signal trail
POST   /orphans/{id}/classify                  {media_type, platform?, library_id?,
                                                dest_override?, parsed?, remember?}
POST   /orphans/{id}/ignore                    {scope: once|forever}
POST   /orphans/{id}/plan                      (re)generate -> Plan
GET    /plans/{id}       GET /plans/{id}/plan.json
POST   /plans/{id}/approve   /reject   /resume   /rollback
POST   /plans/{id}/undo                        distinct from rollback (§6.7); PLAN_UNDONE
GET    /history?since=   GET /events?since=&code=   GET /events/stream   (SSE)
GET    /config           PUT /config           GET /config/export        (YAML, redacted)
GET    /metrics                                Prometheus, if enabled
```

**A security note that belongs in the design, not just the README:** this UI lets an authenticated
user specify arbitrary destination paths for a process with broad filesystem access. That is remote
arbitrary file *write*, by design. Consequences: auth is mandatory; passwords are hashed
(argon2id), never stored plainly; session cookies are `HttpOnly` + `SameSite=Lax`; CSRF tokens on
state-changing requests; and the documentation says in plain words — **do not expose this to the
internet.**

---

## 10. Safety, failure handling, observability

### 10.1 Invariants

Numbered, non-negotiable, and each with a test — because invariants that live in prose get
refactored away. A test suite asserting **I1–I14** against the fault-injecting `fsx.FS` is the
highest-value test in the project.

| # | Invariant |
|---|---|
| **I1** | Never modify, move, delete, rename, `chmod`, `chown`, or truncate a source file. |
| **I2** | Never overwrite, or `rename`/`link` over, an existing destination path. No overwrite policy exists. |
| **I3** | Never treat `content_path` as a unit of work. Work units come from `torrents/files`. |
| **I4** | Never act on a candidate whose resolved paths overlap a **categorized** torrent on any client. Overlaps among uncategorized torrents collapse into one plan. **Overlap is path-equal OR `(dev, ino)`-equal OR `content_fingerprint`-equal** (amended 2026-08-07 — see §3.1 O10). |
| **I5** | Never process a torrent any of whose resolved paths lies inside a configured library root. |
| **I6** | Never create a path outside a configured library root. `filepath.Clean` **and** a root-prefix assertion **and** a symlink-safe resolution of the destination path (`openat2(RESOLVE_NO_SYMLINKS)`, or an `O_NOFOLLOW` walk). The first two are purely lexical, so a symlinked *intermediate directory* inside a library root defeats both. Containment only — I2 still prevents overwriting whatever is found there. |
| **I7** | Every filesystem mutation is journalled before it is attempted, and appended to the flat NDJSON journal after it succeeds. |
| **I8** | The only write to a download client is `addTags`. `setCategory`, `setLocation`, `delete`, `stop`, `recheck` are not implemented. |
| **I9** | Never execute a plan without explicit human approval when **either** the classification score **or** the parse confidence (§4.7) is below the auto threshold, or when `cardinality == multi`, or `mixed`, or the torrent is rootless. |
| **I10** | In dry-run, zero write syscalls occur outside `/config` — **except user-initiated capability probes** (§5.8 case-sensitivity, §6.3 hardlink, §8.3 writability), which write a single named probe file into a library root and remove it. Those are configuration actions, not plan execution, and §9 makes them re-runnable outside the wizard. The carve-out is explicit so the I10 test asserts the real rule rather than being written to look the other way. |
| **I11** | Unknown client states, unknown extensions, unknown platforms, unparseable names, and unmapped paths all fail **closed** — into review, never into a guess. |
| **I12** | No mode or ownership change is ever applied to a **non-directory** path with `st_nlink > 1`. Directory modes are set only on directories Orphanarr created (`plan_step.created_dirs_json`). `chown` is never performed at all. |
| **I13** | A copy is never published without re-`stat`ing its source and proving `src_size`/`src_mtime`/`src_dev`/`src_ino` unchanged since the copy began. |
| **I14** | A download client that cannot express *"this item has no category"* is **refused at configure time and never scanned.** |

I1 is mechanically checkable: `fsx.FS` refuses writes to registered source roots, and the refusal
is unit-tested. **Its refusal set covers `Chmod` and `Chown`, not only writes** — I12 permits
`chmod` on a path with `st_nlink == 1`, which describes an ordinary non-cross-seeded source, so this
guard is what stands between that and a modified source. Do not delete it on the grounds that
§11.1 recommends `:ro`: a read-only mount is a deployment property we cannot enforce from inside.

**I12 is scoped to non-directories deliberately, and this must not be "simplified."** A POSIX
directory is never `st_nlink == 1` — it is 2 (itself plus its parent's entry) plus one per
subdirectory — and a directory cannot be hardlinked at all (`EPERM`, `#C6`), so a link-count test
carries no safety information about one. The unscoped form of this rule forbids the `dir_mode` §6.4
requires, leaving library directories at `0700` under the container umask with correctly-moded,
still-invisible files inside: §6.4's "#1 silent failure" reproduced one level up, inside the rule
written to dissolve it. It fails closed, so it loses no data. `[UNVERIFIED]`, reported by two
agents: btrfs returns directory `nlink == 1`, which would hide this on a btrfs dev box and break it
on the user's ext4. *This defect was introduced by the 2026-08-07 distillation, existed in none of
the five agent proposals, and was caught independently by three agents at the vote.*

**I14 works because of a type choice, not a check.** `Item.Category` is `*string`, so nil — Go's
zero value — means "cannot express categories" means never an orphan. A careless adapter therefore
files **nothing**. The motivating case: a stock Deluge has `enabled_plugins: []`
(`deluge/core/preferencesmanager.py:82`), so **every** torrent reads as uncategorised, which under
O1 is the user's entire seeding library — and under copy-only, copied.

A test suite asserting I1–I14 against the fault-injecting `fsx.FS` remains the highest-value test
in the project.

### 10.2 Guardrails

- `max_plans_per_run` (default 25). **Byte-blind** — 25 plans can be 25 GB or 25 TB; §6.5's
  per-plan `Statfs` is what actually bounds the damage.
- Free-space preflight per destination **filesystem**, re-checked by the executor before each plan
  and at each step boundary, against a committed-bytes ledger. Abort whole-plan, never partial. §6.5.
- **Global kill switch honoured at buffer granularity inside a copy, not only at step boundaries.**
  A step used to be one `link(2)`; it is now a multi-gigabyte copy, so "the next step boundary"
  could be two hours away. The copy loop takes a `ctx`, and cancellation is added to the triggers
  that unlink the partial. SIGTERM is a first-class failure for the same reason — a 60 GB step
  against Docker's ~10 s stop grace needs a shutdown contract, not an assumption.
- Auto-pause after 3 consecutive plan failures, with a Health warning.
- Per-client circuit breaker with exponential backoff (30 s → 15 min).
- Re-read the torrent immediately before executing; abort if it left the predicate.

### 10.3 Failure handling

| Failure | Response |
|---|---|
| Client unreachable | Backoff, mark `unreachable`, **other clients keep polling**, Health warning. Existing orphans are **not** marked gone — absence of evidence is not evidence of absence. |
| Client login 403 | **Stop polling that client entirely.** Manual Test required. Never auto-retry credentials. §3.7 |
| Session 403 (non-login) | Re-login once, retry once. A second 403 is an auth failure. |
| Unexpected API shape | Log the raw payload at debug, mark the client degraded, **do not guess**. Record the observed `webapiVersion`. |
| Mapped path missing | Torrent → `unmappable`, surfaced with a **counter on the dashboard**. Never create it. |
| `EXDEV`/`EPERM`/`EMLINK` on link | Fall back to copy if the library allows it; record the errno; surface in the plan summary. |
| `ENOSPC` mid-copy | Unlink the partial, halt the plan, no auto-rollback, Health warning. **Now the primary failure path, not an edge case** — the failed-plan screen must state how many bytes a rollback would free. Proven reachable: `#C33`. |
| `EDQUOT` mid-copy | Distinct from `ENOSPC` in `write(2)` and must be reported as itself. A panel showing 4 TB free next to *"out of space"* makes a bug report unfixable. |
| `EROFS` on a library write | *"Your library root is mounted read-only."* A predictable mistake immediately after §11.1 teaches users to mount downloads `:ro`. |
| `ENAMETOOLONG` | The §5.8 budget was exceeded despite truncation — surface the offending component and its byte length, not just the errno. |
| `EACCES` opening a source | `SRC_UNREADABLE`, blocking, before execution. There is **no link fallback**: `safe_hardlink_source()` requires read *and* write. §6.4. |
| `SRC_CHANGED` at publish | The source moved under a copy in flight (I13). Discard the partial, fail the step, surface it. Never publish. |
| Destination exists, different content | Collision policy. Plan → `partial`, which is visible and not success. |
| Crash mid-plan | `Reconcile()` on startup. §6.7 |
| Classifier panics | Recovered per-orphan with the stack logged; that orphan → `failed`. One bad file set must never kill the worker or the process. |
| DB corrupt | Refuse to start with a clear message pointing at `/config/journal/` and the nightly backup. |
| Clock skew / future `completion_on` | Treated as not settled; skipped with a warning. |
| Invariant violation | Abort, roll back, log at `error`, raise a Health warning. **This is a bug; treat it like one.** |

### 10.4 Observability

- **Structured logs** (`log/slog`), JSON to `/config/logs/`, human-readable to stdout for
  `docker logs`. Every line carries `client_id`, `infohash`, `plan_id` and a **stable `code`** so
  users can grep and we can document.
- **Every classification logs its full signal breakdown**, not just the winner. When a user reports
  *"it filed my album as an audiobook,"* the log must already contain the answer.
- **Every filesystem failure logs `errno`**, not a stringified exception. `EXDEV` vs `EEXIST` vs
  `ENOSPC` vs `ENAMETOOLONG` are four different user-facing problems with four different
  remediations, and *"failed to link file"* distinguishes none of them.
- `GET /api/v1/health` (liveness, for `HEALTHCHECK`) and `GET /api/v1/system/status` (versions,
  client reachability, device map, hardlink matrix, free space, dry-run flag). Status is what a
  monitoring check should watch.
- `GET /api/v1/orphans/{id}/explain` — the full signal trail. A debugging endpoint *and* a UI
  feature, and the thing that makes *"why did it think that was a comic"* answerable by the user
  instead of by a maintainer reading logs.
- Nightly `VACUUM INTO /config/backups/`, keep 7.
- Prometheus `/metrics`, behind `server.metrics: true`, off by default:
  `orphanarr_orphans{state,type}`, `orphanarr_plans_total{status}`,
  `orphanarr_bytes_linked_total`, `orphanarr_bytes_copied_total`,
  `orphanarr_hardlink_fallback_total{errno}`, `orphanarr_unclassified_total{reason}`,
  `orphanarr_client_up{client}`, `orphanarr_scan_duration_seconds`,
  `orphanarr_library_bytes_available{filesystem}`.
- **§9's Storage screen carries an orphaned-partials row** — *"N files, X GB"* — reconciled against
  the journal. Under copy-only a stray partial is a full-size file, so the bytes should become
  *more* visible, not less.
- **Intra-step byte progress** on the API and the SSE stream. A step used to complete in a syscall;
  a UI that shows nothing for two hours is indistinguishable from a hung one.

### 10.5 Event codes and the webhook

`event.code` is a **stable, documented vocabulary**. It is load-bearing three times over: §10.4
requires it on every log line, §9's dashboard skip-reason breakdown is built entirely from it, and
it is the webhook's filter key. It is enumerated here so it does not have to be reinvented during
implementation.

```
SCAN_STARTED  SCAN_COMPLETED  SCAN_FAILED  CLIENT_UNREACHABLE  CLIENT_AUTH_FAILED
CLIENT_IP_BANNED  CLIENT_REDIRECT
ORPHAN_DISCOVERED  ORPHAN_GONE  SETTLE_PENDING  UNSTABLE_CONTENT
SKIP_NO_METADATA  SKIP_INCOMPLETE  SKIP_PARTIAL_SELECTION  SKIP_QB_MARKER  SKIP_EXCLUDED
SKIP_IGNORED  SKIP_CATEGORIZED  SKIP_UNSUPPORTED_FORMAT  SRC_UNREADABLE
CLASSIFIED  CLASSIFY_AMBIGUOUS  CLASSIFY_MIXED  CLASSIFY_UNKNOWN  CLASSIFY_MANUAL
NEEDS_EXTRACTION  UNKNOWN_PLATFORM
CROSS_SEED_BLOCKED  ROOTLESS_TORRENT  SEEDING_FROM_LIBRARY
PATH_UNMAPPED  PATH_MAPPING_SUSPECT  PATH_NOT_FOUND
PLAN_CREATED  PLAN_APPROVED  PLAN_REJECTED  DISK_SPACE_BLOCKED  PERMISSION_WARNING
STEP_STARTED  STEP_DONE  STEP_SKIPPED  STEP_FAILED  HARDLINK_FALLBACK
COLLISION_DETECTED  SRC_CHANGED  DEST_CHANGED
PLAN_COMMITTED  PLAN_PARTIAL  PLAN_FAILED  PLAN_ROLLED_BACK  PLAN_UNDONE
RECONCILE_REPAIRED  INVARIANT_VIOLATED
```

**The webhook** is the entire notification story (§1.2): one configured URL, one `POST` per event,
JSON body `{code, ts, level, client_id, infohash, plan_id, message, data}`, with a configurable
subscribed-code list defaulting to `PLAN_COMMITTED`, `PLAN_FAILED`, `PLAN_PARTIAL`,
`CLIENT_UNREACHABLE`, `CLIENT_AUTH_FAILED`, `INVARIANT_VIOLATED`. Config:
`ORPHANARR__WEBHOOK__URL` and `ORPHANARR__WEBHOOK__EVENTS`. Roughly forty lines, and it lets users
bolt on whatever relay they already run instead of asking us for a provider matrix.

**The webhook is never on the executor's critical path.** Fire-and-forget onto a bounded buffered
channel with a 5-second timeout, at most one retry, and a drop-with-a-logged-`WARN` when the
buffer is full. A user's unreachable Discord relay must not stall a file operation or wedge a plan.

The `SKIP_*` codes above exist because §9's dashboard skip-reason breakdown is built entirely from
this vocabulary, and the most common real-world outcome is *"Orphanarr isn't doing anything."*
Every clause of the §3.1 predicate that can silently exclude a torrent needs a code, or that panel
cannot answer the question it exists to answer.

### 10.6 Testing

Not an afterthought; a design requirement.

1. **`tests/corpus/` is the classifier's specification.** 118 JSONL cases + 14 tree shapes + 13
   wire samples today; ≥300 before v1. Every misclassification anyone ever reports becomes a row.
   Pure functions, no filesystem, no network, runs in under a second — which is what makes it get
   run. `corpus_lint.py` enforces that every entry justifies itself and that ≥25% expect a negative
   result.
2. **`fs-semantics` CI job.** Loopback ext4 image + tmpfs + a second device to force real `EXDEV`,
   plus the unprivileged-user-namespace bind-mount test, **plus `copy_semantics_test.py` and
   `readonly_mount_test.sh`** — all four scripts, 44 claim IDs. The last two were added by the
   2026-08-07 amendment and wiring them in is not optional: `#C33` is a real `ENOSPC` mid-copy, and
   `ENOSPC` is now the *primary* failure path rather than an edge case. Leaving them as run-once
   artifacts would mean the design's most load-bearing filesystem claims have no regression net.
   Asserts I1–I14 against **real filesystems, not mocks** — mocked filesystem tests would have
   hidden every hazard in §6.3. If a runner cannot provide two filesystems the script exits 2, and
   **that skip must be visible, not silent. Do not delete this test to make CI green.**
   The `#C9`-derived no-clobber collision test must run against **every** filesystem the job builds:
   §6.5's publish ladder now executes on 100% of placements, so its weight went up when hardlinking
   was demoted, not down.
3. **qBittorrent contract tests** against captured real responses from 4.6.x and 5.x instances,
   including the state rename, a session-expiry relogin, a partially-selected torrent, and every
   trap in §3.4. Plus an opt-in `--live` mode, read-only, against a real server, which also
   settles the `save_path` vs `actualStorageLocation()` assumption in §3.2.
   **Those captures do not exist yet** — the current fixture is derived from source, as Appendix C
   states. Capturing them from a real 4.6.x and a real 5.x instance is an open task, not a
   satisfied requirement.
4. **Crash-recovery test** — fork a helper that `os.Exit(1)`s between steps, restart, assert
   `Reconcile()` repairs it. **The single most valuable test in the repo.**
5. Coverage gate ≥80% on `classify`, `layout`, `plan`, `exec` only. No global gate — a global gate
   just produces tests for the HTTP handlers nobody doubts.

---

## 11. Packaging

### 11.1 Docker

Three stages, **no QEMU** — the Go stage runs on `$BUILDPLATFORM` with `GOARCH=$TARGETARCH`, so the
arm64 image is cross-compiled at native speed. This is a concrete reason for the language choice,
not a rationalization of it.

```dockerfile
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /w
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH VERSION COMMIT
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /w/dist internal/web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT" \
    -o /out/orphanarr ./cmd/orphanarr

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec
COPY --from=build /out/orphanarr /usr/local/bin/orphanarr
COPY docker-entrypoint.sh /entrypoint.sh
ENV PUID=1000 PGID=1000 UMASK=002 ORPHANARR__CONFIG_DIR=/config
VOLUME /config
EXPOSE 8790
HEALTHCHECK --interval=30s --timeout=5s \
  CMD wget -qO- http://127.0.0.1:8790/api/v1/health || exit 1
ENTRYPOINT ["/entrypoint.sh"]
CMD ["orphanarr"]
```

**The entrypoint supports both idioms and forces neither** (~15 lines): if running as root with
`PUID`/`PGID` set, ensure the user/group exist, `chown` **`/config` only** — never a media root,
which would be a 40 TB `chown` on startup and a violation of I1 — set the umask, then
`exec su-exec`. If already running non-root (`docker run --user 1000:1000`, or `user:` in compose),
skip all of it and just set the umask. Alpine + `su-exec` costs ~6 MB over distroless and buys the
ecosystem convention that removes an entire category of permission tickets. *(Dissent: D3.)*

**Two documented topologies, and the user picks one, because they are mutually exclusive.** This
replaces the single recommendation that stood before 2026-08-07, when BRIEF §5 A3 established that
the container mounts the clients' download folders and A1 made copy the primary path.

**A — one data mount. Keeps hardlinking available.**

```yaml
services:
  orphanarr:
    image: ghcr.io/stevengann/orphanarr:latest
    environment: [PUID=1000, PGID=1000, UMASK=002, TZ=America/Chicago]
    volumes:
      - ./config:/config
      - /mnt/pool:/data          # ONE mount: /data/torrents AND /data/media below it
    ports: ["8790:8790"]
```

> Split `/downloads` and `/media` mounts make hardlinks impossible **inside** the container even
> when they are one filesystem outside it — and the `st_dev` check everyone writes to detect that
> returns "fine". §6.3.

**B — read-only download mounts. Makes I1 a kernel guarantee; gives up hardlinking permanently.**

```yaml
    volumes:
      - ./config:/config
      - /mnt/pool/media:/data/media
      - /mnt/pool/torrents:/data/torrents:ro     # source can never be written, by the kernel
      - /mnt/seedbox-b/torrents:/data/torrents-b:ro
```

> `:ro` blocks every write path that could reach a source — all 18 constructed in `#R1`, including
> re-opening through `/proc/self/fd` and `O_TMPFILE`. **But a `:ro` bind is itself a separate
> vfsmount, so it returns `EXDEV` unconditionally** (`#R2`) — including when everything is on one
> filesystem behind one `-v`. You cannot have both. BRIEF §5 Q32.

**Neither topology removes the `fsx.FS` source-root guard.** `:ro` is a deployment property we
cannot enforce from inside the container, the guard is ten lines, and this program touches
irreplaceable files. Its refusal set covers `Chmod` and `Chown`, not only writes (§10.1).

**Startup checks that exist because of these two shapes:**

- Warn (Health) when a configured **download** root is writable — `ST_RDONLY` is one `statfs`, and
  it tells a topology-B user that their `:ro` did not take.
- Refuse to start when a **library** root is read-only: that is `EROFS` on every placement, and it
  is the predictable mistake immediately after this section teaches users `:ro`.
- Warn when a download root is an **empty directory**: `#R5` shows a non-recursive bind presents
  exactly that where a disk of downloads lives, and "zero orphans found" is otherwise
  indistinguishable from success.
- Warn when `/config` shares a filesystem with a library root (§6.5 reserves against it).

Port 8790 is adjacent to Readarr's 8787 and unclaimed among the apps checked (Sonarr 8989, Radarr
7878, Lidarr 8686, Readarr 8787). Arbitrary and trivially changed; not a hill.

### 11.2 GitHub Actions

**`ci.yml`** (PR + push): `go vet`, `golangci-lint`, `go test -race ./...`, the **corpus** job, the
**fs-semantics** job (§10.6), `npm ci && npm run build`, and a `docker build` without push.
`-race` matters — the scanner is concurrent across clients and shares a store with the executor.

**`release.yml`** (tag `v*.*.*`): `setup-buildx` (no `setup-qemu`), `metadata-action` for tags,
`build-push-action` with `platforms: linux/amd64,linux/arm64`, `provenance: mode=max`, GHA layer
cache → `ghcr.io/<owner>/orphanarr`. SBOM (SPDX) and keyless cosign signing via OIDC. Raw
`linux/amd64` and `linux/arm64` binaries attached to the GitHub Release — free with Go, and some
people will run this without Docker and are not wrong to.

Tags: `X.Y.Z`, `X.Y`, `X`, `sha-<short>`. **`:latest` is published only from a version tag; `main`
publishes `:develop`.** Homelabbers pull `latest` and expect it not to eat their library on a
Tuesday. Third-party actions pinned by commit SHA; `permissions` minimal; Dependabot for gomod,
npm, docker and actions.

---

## 12. Open questions for the stakeholder

Consolidated into **`docs/BRIEF.md` §5**. **Q1–Q4 were answered on 2026-08-06** and their
consequences are applied throughout this document — see the amendment changelog at the top. What
now blocks or reshapes work:

- **Q25 — Does copy-only push post-file source cleanup into v1?** §0 finding 3: nothing in this
  design ever returns disk space, so the lifetime ceiling is `free − reserve`, once. On a
  realistically-full array v1 does not terminate. This is the largest open question in the project
  and it is not a preference — it decides whether the tool completes its job. Sub-question: should
  Orphanarr refuse to file below a free-space threshold, and what threshold.
- **Q7 — Is any \*arr running with a blank category?** *Promoted to blocking-class 2026-08-07.*
  §3.4 FP-3's mitigation list lost the item that made this survivable, and BRIEF Q27 adds a second,
  less-hardened program (Listenarr) that can trigger the same race. The answer decides whether
  read-only \*arr queue exclusion (D9) is v1 or v1.1.
- **Q29 — What uid/gid does each media server run as, and is there a common group?** Reached
  independently by all five agents. `chown` is unavailable (`CAP_CHOWN`, D3), so mode + shared group
  + umask is the *only* lever, and §6.4's fix cannot be configured without this.
- **Q32 — Read-only download mounts, or hardlinks?** They are mutually exclusive (`#R2`) and the
  design currently ships advice for both. §11.1.
- **Q33 — What uid/gid and umask does each qBittorrent instance write with?** If any writes `0600`,
  Orphanarr can neither copy nor link it, and there is no fallback. §6.4.

Q26, Q28, Q30, Q31, Q34–Q38 configure rather than block; see BRIEF §5.

---

## Appendix A — Sourced facts this design depends on

Everything here was verified on **2026-08-06** against official documentation, project source, or a
test in `tests/verification/` run on the development machine. Full ledger with citations:
`team/notebooks/fact-checker.md` and `team/notebooks/arr-expert.md`.

**Refuted claims — folklore this design deliberately contradicts:**

| Claim | Reality |
|---|---|
| Login success returns `200` with body `"Ok."` | 5.x returns **200 with an empty body**; failure is **401**. Authenticate on status code + `SID` cookie. |
| `state` includes `pausedUP`/`pausedDL` on 5.x | Source emits `stoppedUP`/`stoppedDL`. The published 5.0 wiki was never updated and also omits `forcedMetaDL`. `stoppedUP` and `queuedUP` remain **distinct** in every shipped release. |
| Both `Referer` and `Origin` absent ⇒ qBittorrent rejects the request | **False, and the usual advice is backwards.** `isCrossSiteRequest()` returns *not cross-site* when both are absent — verbatim source comment: *"lets be permissive here."* Injecting `Referer` moves you into the branch that must match `Host`, which a reverse proxy rewrites. Send neither by default. |
| Auth rejection is a 403 | **401.** 403 means two different things split by endpoint: on `auth/login` it is the `WebUIMaxAuthFailCount` **IP ban** (thrown only from `validateCredentials()`); on a non-login endpoint it is an **absent or expired session**. The design branches on both (§3.7). |
| An unrecognised `filter=` value errors | Returns **everything** (`parseTorrentStatus()` ends `return TorrentFilter::All`). A further reason never to trust a server-side filter to have been honoured. |
| `tags` is comma-concatenated | Joined with `", "` — **comma and space**. `split(",")` silently breaks any opt-out tag. |
| `completion_on` is always a valid timestamp | **`-1`** when qBittorrent never observed completion — i.e. migrated clients, an orphan population the brief names. |
| A category is a flat token | **Hierarchical.** `movies/4k` is legal. |
| `progress == 1 && amount_left == 0` ⇒ the on-disk tree matches the torrent | Progress is over *wanted* bytes. Deselected files give `size < total_size` at `progress == 1`. |
| `content_path` is the root folder or the file | Third branch: **the save path**, whenever the torrent has no common root folder. |
| Equal `st_dev` means hardlinking will work | **False.** `filename_linkat()` compares vfsmount pointers. Reproduced: identical `st_dev`, `EXDEV`. |
| `chmod` on a hardlink doesn't affect the other name | **False.** Permissions live on the inode. |
| A hardlink is a snapshot | **False.** One inode; in-place mutation corrupts the seeding copy. |
| Directories can be hardlinked | **False** (`EPERM`). Walk the tree, link each file. |
| `: " < > \| ? *` are illegal in filenames | **False on POSIX** — only `/` and NUL are. They are illegal on SMB/NTFS/exFAT, so a Linux-only test suite will not catch them. |
| RomM's PlayStation slug is `ps` (folder-structure example) or `ps1` (Supported Platforms table) | **`psx`**, per RomM's `UniversalPlatformSlug` enum — which is authoritative because the scanner matches the folder name against it. It is a `StrEnum`: the folder must equal the **value** (`psx`), not the member name (`PSX`). **All three published values disagree and neither documented one works.** |
| `cp --reflink=always` is universally available | Needs btrfs / XFS-with-reflink / ZFS, same-filesystem only. |
| `filter=completed` means "download finished" | `isCompleted()` is state-derived and returns true for `CheckingUploading`. |
| Setting a torrent's category is harmless bookkeeping | Under AutoTMM with a category save path, it **physically relocates the data**. |
| `setLocation` just moves files | It also calls `setAutoTMMEnabled(false)` — silently changing the user's torrent-management mode. |
| Dropping an `.epub` into a Calibre library adds the book | It does not. Calibre's manual says manually added files *"may be automatically deleted."* |
| Komga makes a Series for each subfolder, whatever the depth *(its own docs)* | **False.** `FileSystemScanner.kt` `postVisitDirectory` L156-158 emits a Series only for a directory **directly containing** ≥1 book file; `scannedSeries` is flat; the name is `dir.name`, the last component only. A directory of directories emits nothing. §5.4, §5.7. |
| Komga sorts books by filename, so volume numbers need zero-padding | **The rule is right; the reason is false.** `SeriesLifecycle.kt:45` uses `CaseInsensitiveSimpleNaturalComparator`, so `v2` precedes `v10` unpadded. Pad for other consumers, not for Komga. |
| Komga's "only EPUB is supported" retracts PDF support | **False.** That sentence (2023-11-29, v1.8.0 eBook-support announcement) scopes the *reflowable-ebook reader* — no MOBI/AZW3/FB2. `MediaProfile` has three members (`DIVINA`, `PDF`, `EPUB`) and `PdfExtractor` still ships two and a half years later. A sentence cannot retract a format the codebase still implements. |
| A staging or partial file in a library is invisible to media servers | **False for RomM**, alone among the six targets: `exclude_single_files()` is an *exclusion* list with no dot-file rule, so it indexes and hashes the partial. §6.5. |
| Deluge always has labels | **False.** `enabled_plugins` defaults to `[]` (`deluge/core/preferencesmanager.py:82`), so a stock instance reports **every** torrent as uncategorised. → I14. |
| A `0600` source can at least be hardlinked if it can't be copied | **False.** `safe_hardlink_source()` requires `MAY_READ\|MAY_WRITE` and systemd ships `fs.protected_hardlinks = 1`. Both paths fail. |
| `chmod` on a copy affects the source | **False** (`#C21`) — the exact converse of the hardlink row above, and the basis of I12. |
| Size + `fsync` proves a copy is correct | **False** (`#C29`). A source mutated mid-copy yields a destination matching neither its old nor its new contents, at the right size, after `fsync`. **A checksum does not catch it either.** → I13. |
| `statfs`'s free-space field is what you can use | **False.** `f_bfree` includes root-reserved blocks — `#C28` measured **11.61 GiB** unavailable to an unprivileged process. Use `Bavail`. |
| A read-only bind mount is a free safety measure | **False.** It is a separate vfsmount, so it guarantees `EXDEV` and forecloses hardlinking permanently (`#R2`). It does buy kernel-enforced I1 (`#R1`). Pick one. |

**Load-bearing positive facts:** Navidrome ignores paths entirely (tags only); Komga makes a Series
for each directory that *directly contains* books, and a Book per file (corrected 2026-08-07 — see
the refuted-claims table above); Audiobookshelf parses author/series/sequence/year/narrator/ASIN
out of the folder name; RomM detects platform from the folder and parses `()`/`[]` tags from the
filename; Plex and Jellyfin agree on `Title (Year)/`, `Season NN`, and `sXXeYY-eZZ` but **disagree
irreconcilably on provider-ID and edition syntax**; a single shared bind mount makes hardlinking
work (`st_nlink=3`).

---

## Appendix B — Conflicts resolved, and the dissent log

Positions that lost. Recorded so the team remembers what it chose against, and what would make it
reconsider. Resolution order per `team/PROCESS.md` §3: facts > cited ecosystem reality > data-loss
risk > simplicity.

| # | Conflict | Ruling | Losing position & what would reopen it |
|---|---|---|---|
| **D1** | Go vs Python | **Go.** 4/5, on image size, no-QEMU arm64 cross-compilation, and direct syscall/errno access. | *Fact Checker* preferred Python for corpus-iteration speed, while explicitly stating no factual objection to Go. Also correctly noted the image-size figures were estimates, not measurements. Reopen if Go's table-driven corpus loop proves materially slower to iterate on in practice. |
| **D2** | SPA vs server-rendered + htmx | **Preact SPA over the JSON API**, hard-capped at six screens, no component library, no state library. **4/5**, plus the concrete argument that the plan-review tree is genuinely stateful and that the API must exist regardless as the automation and test surface. | *Old Man*, alone: a Node toolchain in CI, a lockfile with hundreds of transitive dependencies, and an `npm audit` treadmill — forever — for six pages. Reopen if frontend build maintenance measurably outweighs the plan-review UX. **The seam is the API: if this flips, nothing else in the design changes.** |
| **D3** | PUID/PGID + Alpine vs distroless nonroot | **Both.** The entrypoint honours PUID/PGID when started as root and skips straight to umask when started non-root. | *Devil's Advocate* argued distroless-only so the container is never root for even an instant. The dual-mode entrypoint gives them `user: "1000:1000"` as a first-class path; the residual cost is the ~6 MB Alpine base and one instant as root for PUID users. |
| **D4** | Job-level staging tree vs per-file staged publish | **Per-file `.orphanarr-partial.tmp` + no-clobber publish. No job-level staging tree in v1.** **(Round-01 wording said "+ rename only"; §6.5 no longer publishes with `rename` and an implementer building from this summary instead of the normative section would ship the exact hazard three agents blocked on. Corrected in round 03 — when a section changes, grep everything that summarizes it.)** **Ruling upheld 2026-08-07 on new grounds, because its original reason died.** *"No partial file is ever visible"* was false even then — RomM indexes it (§6.5) — and the exposure window went from microseconds to the duration of a multi-gigabyte copy. What replaces it is a mechanism argument: **`rename(2)` onto a non-empty directory returns `ENOTEMPTY`/`EEXIST`, and §5.2 says merging into an existing series folder *is the normal case* — so a staging tree cannot express the normal case.** A zero-machinery variant (copy all partials, then publish all) was found and argued against by the agent who found it: it trades a cosmetic transient for losing N files of copying on a crash instead of one, and `Reconcile()`'s sweep would delete all of it. | *Arr Expert*: media-server scanners watch these directories, and a half-built season folder can be scanned, half-matched, and **cached**. Reopen on evidence that Plex or Jellyfin caches a bad match from a partially-populated folder — that is a concrete, testable claim. **Still untested, open since round 01, and A1 raised its exposure five orders of magnitude.** The remedy is deliberately not adopted until the hazard is demonstrated. |
| **D5** | Checksum copies by default | **Size + `fsync` by default; BLAKE3 opt-in, copies only.** | *Senior Dev* and *Old Man* wanted checksums on by default — and the first draft of this row **misdescribed the position it rejected**. The Senior Dev proposed hashing **inline as the copy streams**, which reads no extra bytes and costs ~0 on a home NAS; only the *re-read* variant costs minutes. The ruling stands on simplicity grounds, but it should be understood as rejecting a nearly-free option, not an expensive one — which makes it a genuinely close call and a cheap thing to turn on later. |
| **D6** | Auto-rollback on a clean mid-plan failure | **Off.** Halt and offer Resume · Roll back · Ignore. Deleting 199 successfully-written files because step 200 hit `ENOSPC` is worse than stopping. | *Old Man* and *Arr Expert* wanted automatic rollback. Their position **wins for unclean exits**: anything `Reconcile()` cannot verify is rolled back and surfaced, never silently resumed. |
| **D7** | Server-side `category=` filter vs fetching all torrents | **Fetch `filter=all`, filter locally.** | *Arr Expert* argued the server-side filter is correct and cheaper at scale — and it is. But it is incompatible with that same agent's own cross-seed mitigation (§3.4 FP-1), which needs *every* torrent including categorized ones. The optimization and the safety requirement cannot both hold; safety wins. |
| **D8** | Music: verbatim folder vs tag-derived `{AlbumArtist}/{Album}` | **Tag-derived two levels, contents verbatim.** 3/5. | *Old Man* and *Devil's Advocate* wanted the album directory placed fully verbatim with no tag reading. Their stronger claim — **never rename tracks, never write tags** — won outright and is now I1. **The round-01 justification for this ruling ("the tags are read anyway for §4.3, so the folder is free") was false**: §4.2 gated S3 on the confidence score, so a well-named album cleared the threshold and was never probed. Round 02 made the audio probe unconditional instead of retracting the ruling, so the cost is now real, paid on every audio payload, and stated in §4.2 — not free. |
| **D9** | Read-only \*arr queue exclusion in v1 | **v1.1 proposal, and its stated support died on 2026-08-07.** v1 ships `ignore_save_paths` globs plus a loud first-run banner. ~~Hardlink-first makes racing an \*arr survivable — the worst case is a duplicate library entry, which is recoverable.~~ **Struck: under copy-only the worst case is a duplicate library entry *plus a full second copy of the payload*.** §3.4 FP-3 already conceded that globs cannot separate a blank-category \*arr's downloads, so this ruling now rests on a banner and detection alone. Three agents reported this independently and all three still recommend keeping the feature out of v1 — an \*arr client is scope creep — but the row must stop claiming a mitigation it does not have. | *Arr Expert* wanted it in v1, with source proving Sonarr polls without a category filter when its category field is blank. **BRIEF §5 Q7 is promoted to blocking-class**; BRIEF Q27 adds a second trigger (Listenarr's `Matches()` returns true for every torrent when its category is blank, and it ships canary-only). |
| **D10** | Rootless torrents: refuse outright vs operate per-file | **Never use `content_path` for any torrent (universal, I3), *and* park rootless torrents in review with a required destination confirmation.** Both gates. | *Devil's Advocate* and *Arr Expert* would let rootless torrents proceed on the enumerated file list; *Senior Dev* and *Fact Checker* would refuse them entirely. The synthesis keeps the universal rule that makes the file list authoritative and keeps the extra human gate for the specific shape that is a rollback nightmare. |
| **D11** | `strict_multi_client` | **Default on.** Scanning and the UI always continue per-client; only **plan creation** pauses while an enabled client is unreachable, because the overlap index would be incomplete. | *Old Man* and *Arr Expert* emphasised that one dead client must never stall the others — which remains true for scanning. The narrower reading (planning only) is what ships. |

| **D12** | Dry-run mechanism | **Do not call the executor.** §2.4. 4/5. | *Old Man* specified `Execute(ctx, plan, commit=false)` — one code path with a flag — for the same stated reason (a separate preview function eventually lies). The principle is unanimous; the mechanism is not, and a flag inside the executor is the shape §2.4 rejects. Added in round 02 after the Devil's Advocate caught the draft claiming unanimity here. |
| **D13** | `keep_larger` collision policy | **Removed.** There is no non-destructive implementation, I2 forbids it three times in the same document, and size is the wrong discriminator (a good x265 loses to a bloated x264). | *Senior Dev* (`replace_if_larger`) and *Arr Expert* (`keep_larger`) offered it; *Old Man*, *Devil's Advocate* and *Fact Checker* explicitly ruled it out. It reached the round-01 draft as a merge artifact — printed as an option directly above the paragraph forbidding it — and was the Devil's Advocate's blocking objection. Reopen only by amending I2 explicitly and stating the size-comparison failure case. |
| **D14** | Config file vs database as source of truth | **Database authoritative, UI is the editor, env overrides at boot render read-only, YAML export for backup.** §8.1. The brief mandates a UI for configuration; a file-authoritative design makes that UI decorative or creates a last-writer-wins fight. | *Old Man* and *Senior Dev* proposed splitting it — a YAML file owning infrastructure, the DB owning runtime decisions, with no key in both. Coherent, and it keeps GitOps users first-class; it lost to the \*arr convention and to the brief's explicit UI requirement. Recorded in round 02; it was silently dropped from the round-01 draft, which PROCESS §2 does not permit. |
| **D15** | Cut the hardlink path entirely vs demote it | **Demote. Copy is primary; hardlinking auto-upgrades per pair as the §6.3 probe passes.** 4–1. | *Old Man*, alone: BRIEF A1 + A3 together mean `link` can never succeed here, so every retained branch is dead code touching inodes shared with seeding torrents — and the cost is not the syscall but the **bimodality tax** across §6.4, §6.6, §6.7, §8.3, §9, §10.4, §10.5. He lost on the reading that *"assume a full copy for now"* is a planning assumption rather than a statement about physics, and on cutting being unrecoverable by configuration. **Not on a facts citation** — an earlier draft claimed PROCESS §3.1 and the agent whose position it supported pointed out §3.1 concerns cited claims, not the interpretation of a sentence. His strongest point is partly adopted: §6.4's chmod bimodality is dissolved structurally, but the tax survives at §6.6, §6.7, §3.5, §8.2, §10.4 and §10.5. Reopen on a defect caused by the retained path, or the stakeholder confirming genuinely separate filesystems. |
| **D16** | `OPS__MODE` default: `copy` vs `link_or_copy` | **Per-pair auto-upgrade**, which is neither side's proposal. | *Senior Dev* and *Devil's Advocate* held that the default should stay `link_or_copy` — attempt-and-fall-back costs one failed syscall per file and beats predicting. *Arr Expert* and *Fact Checker* wanted `copy`. The first distillation recorded all four as agreeing on `copy`, which PROCESS §2 forbids; the split is recorded here. The per-pair upgrade moots most of the disagreement. |
| **D17** | `verify_copies` on by default (D5, re-voted) | **Default stays off.** | *Arr Expert*, *Devil's Advocate* and *Fact Checker* voted to flip it on 2026-08-07 — 3–2 *against* the standing ruling — on the grounds that D5's *"copies only"* qualifier stopped being a narrowing once copies became 100% of placements. Upheld under PROCESS §3.4 (simplicity breaks ties), and **none of the three pressed it**, because `#C29` — the Fact Checker's own finding — shows a checksum does not catch the mutating-source failure that actually motivated the re-vote. I13 addresses that at two `stat` calls. The Fact Checker reported his own un-declined recommendation against his interest after the first draft recorded all three as declining. |
| **D18** | Is the old `{Author}/{Title}/{Title}.ext` ebook tree actually broken? | **Replaced anyway** (§5.7). | *Fact Checker* graded it `[VERIFIED — safe]` for standalone books and `[PARTIAL]` only for multi-volume works: the `{Author}` directory holds no book files, so it emits no series and causes no empty-series pollution — the level is **inert**, not harmful. *Arr Expert* called the same tree "the absence of a layout." Both are right about different things, and the first distillation recorded only the second. **Nothing was invisible under the old tree**, so §5.7 is series-hygiene and idiom, not a data-loss fix, and should not be cited as one. |

**Notable non-conflict.** Both the Old Man and the Devil's Advocate wrote extensive pre-rebuttals
defending "no external metadata providers in v1" against attacks they expected from the Senior Dev
and the Arr Expert. **Neither attack came.** All five independently reached the same conclusion,
the Arr Expert most emphatically. Per PROCESS §6.5 this is recorded as settled, and the falsifiable
trigger both agents volunteered stands: **a corpus of real orphan release names where name-only
parsing misfiles more than 1 in 20 movie/TV items reopens it — for movies and TV only, as optional
confidence enrichment, never as a hard dependency.**

---

## Appendix C — Test artifacts already in the repo

| Artifact | Proves | Status |
|---|---|---|
| `tests/verification/fs_semantics_test.py` | 19 claims (`#C1`–`#C19`) about `link`/`rename`/`replace`/copy/`NAME_MAX`/permissions/reflink against two real filesystems. The regression net for every assertion in §6. **Triaged after the 2026-08-07 amendment: 7 become hardlink-conditional, 5 change role, 7 are unconditional, none becomes wrong.** | 19 checked, 0 refuted |
| `tests/verification/bindmount_hardlink_test.sh` | Two bind mounts of one filesystem report identical `st_dev` **and** fail `link()`; a single shared mount succeeds. Unprivileged user+mount namespace — no Docker, no root. | 4 checked, 0 refuted |
| `tests/verification/copy_semantics_test.py` | **15 claim IDs (`#C20`–`#C34`), 17 checks** — `#C23` and `#C24` each carry two. The copy path is no longer the fallback but the only path, so this covers what §6.5 now rests on: `chmod`-on-copy not touching the source (`#C21`), mode surviving the `link`+`unlink` publish (`#C26`), `chown` needing `CAP_CHOWN` (`#C23`), umask stripping the mode argument of both `open(2)` and `mkdir(2)` (`#C24`), setgid roots (`#C25`), `Bavail` vs `f_bfree` (`#C28`, **11.61 GiB** of root-reserved space), sparse inflation (`#C31`), a real `ENOSPC` mid-copy (`#C33`), `O_EXCL` on the partial (`#C34`), and the mutating-source class (`#C29`) that produced I13. | 17 checked, 0 refuted |
| `tests/verification/readonly_mount_test.sh` | 6 claims (`#R1`–`#R6`) on read-only bind mounts, unprivileged namespace, no Docker, no root. `:ro` blocks all 18 constructed write paths including `/proc/self/fd` re-open and `O_TMPFILE` (`#R1`) — but does **not** reach nested submounts (`#R4`), stop a symlink escaping (`#R3`), or survive a second writable mount of the same data (`#R6`); and it guarantees `EXDEV` (`#R2`). A non-recursive bind shows an **empty directory** where a disk of downloads lives (`#R5`). | 6 checked, 0 refuted |
| `tests/verification/qbittorrent_contract_test.py` | Fixture fields all exist in `serialize_torrent.h`; the reference orphan predicate produces the documented outcome for 13 wire samples including every trap; `split(",")` on tags silently drops an exclusion tag. `--live` mode re-runs read-only against a real server. | PASS, 0 violations |
| `tests/verification/corpus_lint.py` | The corpus is well-formed, IDs unique, every entry justifies itself, ≥25% negative expectations. | PASS — 118 entries, 27% negative |
| `tests/corpus/*.jsonl` + `folder_shapes.json` + `qbittorrent_info_samples.json` | 118 cases, 14 tree shapes, 13 wire samples covering movies, TV, music, comics, ROMs, ebooks, audiobooks, ambiguous, and adversarial inputs. | Seeded; target ≥300 before v1 |

**Totals: 40 numbered claim IDs / 46 checks across four filesystem and mount scripts, 0 refuted.**

> **This tally has now been wrong SIX times, including twice in the sentence written to stop it
> being wrong.** "44 IDs" silently counted the bind-mount script's four *unnumbered* checks as
> though they were IDs; the correction to "44 / 45" was itself wrong twice over.
>
> Stop writing the number down. **Every script prints its own count, and `corpus_lint.py` prints
> the corpus figures.** Quote the output. §0's standing instruction — trust the citation beside a
> number, not the number — applies to this table more than anywhere else in the document, because
> this table is the one whose entire job is the number.

**Two artifacts require re-pinning when §5.8 and §6.5 land:** `#C30` hard-asserts the old 18-byte
suffix and 237-byte budget, which `.orphanarr-partial.tmp` changes to 22 and 233 — derive it as
`255 − len(suffix)` rather than hard-coding a second wrong constant. And `#R7` (one host directory,
two bind mounts: identical `dev`/`ino`, differing paths, `link` still `EXDEV`) is measured and
parked, pending. It was deliberately **not** landed during the amendment vote, because adding it
would have falsified the claim count being certified in the same document.

`qbittorrent_info_samples.json` is **derived from source, not captured from a live server**, and is
labelled as such. Replacing it with real captures from a 4.6.x and a 5.x instance is an open task.
