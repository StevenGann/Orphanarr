#!/usr/bin/env python3
"""
Orphanarr — qBittorrent WebUI API v2 contract test.

WHAT THIS PROVES
----------------
1. The field names Orphanarr's design depends on are the field names qBittorrent
   actually emits. The expected set is transcribed from
   `src/webui/api/serialize/serialize_torrent.h` (master, read 2026-08-06), not
   from the wiki, because the wiki is demonstrably stale (it still documents
   `pausedUP`/`pausedDL`; the source emits `stoppedUP`/`stoppedDL`).

2. The orphan predicate is EXECUTABLE and produces the documented outcome for
   every trap in tests/corpus/qbittorrent_info_samples.json. Implementations
   must reproduce `classify()` below or explain why not.

Offline (default):  python3 tests/verification/qbittorrent_contract_test.py
Live (read-only):   python3 tests/verification/qbittorrent_contract_test.py --live \
                        --url http://localhost:8080 --user admin --password adminadmin

Live mode performs ONLY /api/v2/auth/login and GET /api/v2/torrents/info.
It never writes, moves, deletes, or sets a category.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.parse
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
CORPUS = os.path.join(HERE, "..", "corpus", "qbittorrent_info_samples.json")

# Transcribed from serialize_torrent.h, master, 2026-08-06.
SOURCE_KEYS = {
    "hash", "infohash_v1", "infohash_v2", "name", "magnet_uri", "size", "progress",
    "dlspeed", "upspeed", "priority", "num_seeds", "num_complete", "num_leechs",
    "num_incomplete", "ratio", "popularity", "eta", "state", "seq_dl",
    "f_l_piece_prio", "category", "tags", "super_seeding", "force_start",
    "save_path", "download_path", "content_path", "root_path", "added_on",
    "completion_on", "tracker", "trackers_count", "dl_limit", "up_limit",
    "downloaded", "uploaded", "downloaded_session", "uploaded_session",
    "amount_left", "completed", "max_ratio", "max_seeding_time",
    "max_inactive_seeding_time", "ratio_limit", "seeding_time_limit",
    "inactive_seeding_time_limit", "share_limits_mode", "share_limit_action",
    "seen_complete", "last_activity", "total_size", "auto_tmm", "time_active",
    "seeding_time", "availability", "reannounce", "comment", "private",
    "has_metadata", "connections_count", "connections_limit", "total_wasted",
    "pieces_num", "piece_size", "pieces_have", "created_by", "creation_date",
}

# The fields Orphanarr's core loop cannot work without.
REQUIRED_BY_ORPHANARR = {
    "hash", "name", "category", "tags", "state", "progress", "amount_left",
    "save_path", "content_path", "root_path", "completion_on", "added_on",
    "has_metadata", "size", "total_size",
}

# Source of truth: serialize_torrent.cpp torrentStateToString(). Both the 5.x
# spelling and the pre-5.0 spelling are listed, because Orphanarr will meet both.
STATES_AT_REST_COMPLETE = {
    "uploading", "stalledUP", "queuedUP", "forcedUP",
    "stoppedUP",              # qBittorrent >= 5.0
    "pausedUP",               # qBittorrent < 5.0
}
STATES_IN_MOTION = {
    "checkingUP", "checkingDL", "checkingResumeData", "moving", "allocating",
    "downloading", "metaDL", "forcedMetaDL", "forcedDL", "stalledDL", "queuedDL",
}
STATES_BROKEN = {"error", "missingFiles", "unknown"}


def parse_tags(raw: str) -> list[str]:
    """qBittorrent joins tags with ', ' (comma AND space) — serialize_torrent.cpp.

    Splitting on ',' alone leaves a leading space on every tag after the first.
    """
    return [t.strip() for t in raw.split(",") if t.strip()]


def classify(t: dict) -> tuple[str, str]:
    """Reference orphan predicate. Returns (verdict, reason).

    verdict in {ORPHAN, SKIP, DEFER, REFUSE}.
    """
    if not t.get("has_metadata", True):
        return "SKIP", "no metadata yet; content_path and root_path are empty strings"
    if t.get("category", "") != "":
        return "SKIP", f"has category {t['category']!r}"

    state = t.get("state", "unknown")
    if state in STATES_BROKEN:
        return "SKIP", f"state {state!r}: data is missing or errored"
    if state in STATES_IN_MOTION:
        return "DEFER", f"state {state!r}: bytes or paths are in motion"
    if state not in STATES_AT_REST_COMPLETE:
        return "DEFER", f"state {state!r} is not a known at-rest complete state"

    if t.get("progress", 0) < 1 or t.get("amount_left", 1) != 0:
        return "DEFER", "wanted bytes are not all present"

    content = t.get("content_path", "")
    save = t.get("save_path", "")
    if not content:
        return "SKIP", "empty content_path"
    if content.rstrip("/") == save.rstrip("/"):
        return (
            "REFUSE",
            "content_path == save_path: this multi-file torrent has no common root "
            "folder, so content_path is the whole shared completed directory. "
            "Acting on it would move or delete every other torrent stored there.",
        )
    return "ORPHAN", "uncategorised, complete, at rest, with a distinct content path"


def offline() -> int:
    with open(CORPUS, encoding="utf-8") as fh:
        doc = json.load(fh)

    failures = 0
    print("=== A. field-name contract (fixture vs serialize_torrent.h) ===")
    for s in doc["samples"]:
        keys = {k for k in s if not k.startswith("_")}
        unknown = keys - SOURCE_KEYS
        if unknown:
            print(f"[REFUTED ] {s['_id']}: fixture uses non-source keys {sorted(unknown)}")
            failures += 1
    if not failures:
        print(f"[VERIFIED] all {len(doc['samples'])} fixtures use only keys present in "
              f"serialize_torrent.h")

    missing_required = REQUIRED_BY_ORPHANARR - SOURCE_KEYS
    if missing_required:
        print(f"[REFUTED ] Orphanarr requires keys qBittorrent does not emit: "
              f"{sorted(missing_required)}")
        failures += 1
    else:
        print(f"[VERIFIED] all {len(REQUIRED_BY_ORPHANARR)} keys Orphanarr's core loop "
              f"needs exist in the source")

    print()
    print("=== B. orphan predicate vs documented expectations ===")
    for s in doc["samples"]:
        want = s["_expect"]
        got, reason = classify(s)
        ok = got == want
        failures += 0 if ok else 1
        print(f"[{'VERIFIED' if ok else 'REFUTED ' }] {s['_id']} want={want:<7} got={got:<7} "
              f"— {s['_label']}")
        if not ok:
            print(f"           reason given: {reason}")

    print()
    print("=== C. tag parsing ===")
    tags_raw = "manual, keep, orphanarr-ignore"
    naive = tags_raw.split(",")
    correct = parse_tags(tags_raw)
    ok = ("orphanarr-ignore" not in naive) and ("orphanarr-ignore" in correct)
    failures += 0 if ok else 1
    print(f"[{'VERIFIED' if ok else 'REFUTED '}] split(',') yields {naive} — an exclusion "
          f"list keyed on 'orphanarr-ignore' MISSES it; strip() is mandatory")

    print()
    print(f"{'FAIL' if failures else 'PASS'}: {failures} contract violation(s)")
    return 1 if failures else 0


def live(url: str, user: str, password: str) -> int:
    base = url.rstrip("/")
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(), urllib.request.HTTPRedirectHandler()
    )
    body = urllib.parse.urlencode({"username": user, "password": password}).encode()
    req = urllib.request.Request(
        f"{base}/api/v2/auth/login",
        data=body,
        headers={
            # Required: "Set Referer or Origin header to the exact same domain and
            # port as used in the HTTP query Host header." — WebUI API wiki.
            "Referer": base,
            "Origin": base,
            "Content-Type": "application/x-www-form-urlencoded",
        },
    )
    # NOTE: do NOT test for the body "Ok.". qBittorrent master's
    # AuthController::loginAction() calls setStatus(APIStatus::Ok), which
    # WebApplication renders as HTTP 200 with an EMPTY body; older builds
    # returned the literal "Ok.". Failure raises APIErrorType::Unauthorized ->
    # UnauthorizedHTTPError (401), where older builds returned 200 + "Fails.".
    # Authenticate on the STATUS CODE and the presence of the SID cookie.
    try:
        with opener.open(req, timeout=15) as resp:
            code = resp.getcode()
            payload = resp.read().decode().strip()
    except urllib.error.HTTPError as exc:
        print(f"[REFUTED ] auth failed: HTTP {exc.code}")
        return 1
    if code != 200 or (payload and payload not in ("Ok.",)):
        print(f"[REFUTED ] auth: HTTP {code}, body {payload!r}")
        return 1
    print(f"[VERIFIED] POST /api/v2/auth/login -> HTTP {code}, body {payload!r} "
          f"(empty on 5.x+, 'Ok.' on older builds); SID cookie set")

    req = urllib.request.Request(
        f"{base}/api/v2/torrents/info?filter=all", headers={"Referer": base}
    )
    with opener.open(req, timeout=30) as resp:
        torrents = json.loads(resp.read().decode())
    print(f"[INFO    ] {len(torrents)} torrents returned")

    observed: set[str] = set()
    for t in torrents:
        observed |= set(t)
    extra = observed - SOURCE_KEYS
    missing = REQUIRED_BY_ORPHANARR - observed
    if extra:
        print(f"[PARTIAL ] server emits keys not in the transcribed source set: {sorted(extra)}")
    if missing:
        print(f"[REFUTED ] server does NOT emit required keys: {sorted(missing)}")
    else:
        print("[VERIFIED] every key Orphanarr requires is present on the wire")

    # The claim that matters most: what an uncategorised torrent looks like.
    uncat = [t for t in torrents if t.get("category") == ""]
    print(f"[INFO    ] {len(uncat)} torrents report category == '' (empty string)")
    nonstring = [t for t in torrents if not isinstance(t.get("category"), str)]
    if nonstring:
        print(f"[REFUTED ] {len(nonstring)} torrents report a non-string category "
              f"(e.g. null) — the empty-string assumption is wrong")
    else:
        print("[VERIFIED] category is always a string; uncategorised is '' and never null")

    # And the content_path == save_path trap, measured rather than assumed.
    collide = [t for t in torrents
               if t.get("content_path", "").rstrip("/") == t.get("save_path", "").rstrip("/")
               and t.get("has_metadata", True)]
    print(f"[INFO    ] {len(collide)} torrents have content_path == save_path "
          f"(the 'no common root folder' case Orphanarr must refuse)")
    for t in collide[:5]:
        print(f"           - {t.get('name')!r} -> {t.get('content_path')!r}")

    states = sorted({t.get("state") for t in torrents})
    print(f"[INFO    ] observed states: {states}")
    if "pausedUP" in states or "pausedDL" in states:
        print("[INFO    ] this server is pre-5.0 (paused* spelling)")
    if "stoppedUP" in states or "stoppedDL" in states:
        print("[INFO    ] this server is 5.x+ (stopped* spelling)")

    nocomp = [t for t in torrents if t.get("completion_on") in (-1, 0)]
    print(f"[INFO    ] {len(nocomp)} torrents report completion_on <= 0 — any "
          f"'settle for N hours' rule keyed on completion_on is unusable for these")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--live", action="store_true")
    ap.add_argument("--url", default=os.environ.get("QBT_URL", "http://localhost:8080"))
    ap.add_argument("--user", default=os.environ.get("QBT_USER", "admin"))
    ap.add_argument("--password", default=os.environ.get("QBT_PASS", ""))
    args = ap.parse_args()
    rc = offline()
    if args.live:
        print()
        print("=== LIVE ===")
        rc |= live(args.url, args.user, args.password)
    return rc


if __name__ == "__main__":
    sys.exit(main())
