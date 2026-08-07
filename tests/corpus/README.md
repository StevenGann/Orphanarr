# Orphanarr Test Corpus

Inputs the classifier and the layout planner will have to survive. Built during
Round 01 (2026-08-06) by the Fact Checker, **before** any implementation exists,
so that "our parser handles X" is a passing test rather than an assertion.

## What each file is

| File | Contains | What it proves / forces |
|---|---|---|
| `movies.jsonl` | Movie release names, dir and file shapes | The movie path: year extraction, edition tags, multi-part, non-ASCII titles |
| `tv.jsonl` | TV release names, season packs, dailies, anime | Season/episode extraction, multi-episode, absolute numbering, date-based |
| `music.jsonl` | Album folder names, scene music releases | Artist/album/year extraction, multi-disc, VA compilations, format detection |
| `ebooks.jsonl` | Ebook filenames and folders | Author/title split, series index, format precedence |
| `audiobooks.jsonl` | Audiobook folders, single-m4b and multi-part | Narrator, series index, disc subfolders |
| `comics.jsonl` | Comic/manga files and folders | Series/volume/issue extraction, oneshots, cbz vs cbr vs pdf |
| `roms.jsonl` | ROM filenames across platforms | Platform inference, No-Intro/GoodTools/TOSEC tags, multi-disc, region codes |
| `ambiguous.jsonl` | Cases where **two or more** media types are plausible | Forces the design to have an explicit tie-break and an "unknown" outcome |
| `adversarial.jsonl` | Hostile inputs — path traversal, control chars, absurd lengths, decoys | Forces sanitisation and refusal-to-act behaviour |
| `folder_shapes.json` | Whole directory trees, not just names | Forces the "what does the torrent actually contain?" question, not "what is it called?" |
| `qbittorrent_info_samples.json` | `/api/v2/torrents/info` objects | Forces the orphan predicate to handle real field shapes, incl. the traps |

## Field contract (JSONL)

```json
{
  "id":        "unique stable id, referenced by verdicts and bug reports",
  "input":     "the literal string or path the classifier receives",
  "shape":     "file | dir",
  "expect":    {"type": "movie|tv|music|ebook|audiobook|comic|rom|unknown", ...},
  "difficulty":"easy | medium | hard | trap",
  "why":       "what specifically this case is here to break"
}
```

`expect.type` of `"unknown"` is a **required correct answer**, not a failure.
A classifier that never says "unknown" is a classifier that will misfile things.

## Provenance and honesty

- Release-name **patterns** are real and widely observable in the wild; the
  specific titles are drawn from well-known public releases or are neutral
  stand-ins that preserve the exact tokenisation problem. Nothing here is a
  copy of any private tracker's index.
- `qbittorrent_info_samples.json` is **derived from qBittorrent source**
  (`src/webui/api/serialize/serialize_torrent.{h,cpp}`, master, read 2026-08-06),
  not captured from a live instance. It is labelled `"provenance": "derived"`.
  Anyone with a running qBittorrent should replace it with a real capture —
  `tests/verification/qbittorrent_contract_test.py --live` does exactly that and
  will fail loudly if the wire disagrees with the source-derived shape.
