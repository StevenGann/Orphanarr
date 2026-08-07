#!/usr/bin/env python3
"""
Orphanarr — corpus integrity check.

WHAT THIS PROVES
----------------
The corpus is machine-readable and well-formed, IDs are unique, every entry
declares an expectation, and — the point of the whole exercise — the corpus
actually contains cases whose correct answer is "unknown"/"refuse". A corpus
made only of easy wins proves nothing.

Run: python3 tests/verification/corpus_lint.py
"""

from __future__ import annotations

import json
import os
import sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
CORPUS = os.path.abspath(os.path.join(HERE, "..", "corpus"))

JSONL = [
    "movies.jsonl", "tv.jsonl", "music.jsonl", "comics.jsonl",
    "roms.jsonl", "ebooks.jsonl", "audiobooks.jsonl",
    "ambiguous.jsonl", "adversarial.jsonl",
]


def main() -> int:
    errors: list[str] = []
    ids: Counter[str] = Counter()
    by_difficulty: Counter[str] = Counter()
    total = 0
    negative = 0

    for fname in JSONL:
        path = os.path.join(CORPUS, fname)
        if not os.path.exists(path):
            errors.append(f"{fname}: missing")
            continue
        with open(path, encoding="utf-8") as fh:
            for lineno, line in enumerate(fh, 1):
                if not line.strip():
                    continue
                try:
                    row = json.loads(line)
                except json.JSONDecodeError as exc:
                    errors.append(f"{fname}:{lineno}: invalid JSON — {exc}")
                    continue
                total += 1
                for required in ("id", "input", "expect", "why"):
                    if required not in row:
                        errors.append(f"{fname}:{lineno}: missing field {required!r}")
                ids[row.get("id", f"<{fname}:{lineno}>")] += 1
                by_difficulty[row.get("difficulty", "unspecified")] += 1
                exp = row.get("expect", {})
                if exp.get("type") in ("unknown",) or exp.get("action") in (
                    "refuse", "defer", "sanitise", "truncate", "collision", "noop"
                ):
                    negative += 1
                if len(row.get("why", "")) < 20:
                    errors.append(f"{fname}:{lineno}: 'why' is too thin to be useful")

    for shape_file in ("folder_shapes.json", "qbittorrent_info_samples.json"):
        path = os.path.join(CORPUS, shape_file)
        if not os.path.exists(path):
            errors.append(f"{shape_file}: missing")
            continue
        try:
            with open(path, encoding="utf-8") as fh:
                json.load(fh)
        except json.JSONDecodeError as exc:
            errors.append(f"{shape_file}: invalid JSON — {exc}")

    dupes = [i for i, n in ids.items() if n > 1]
    if dupes:
        errors.append(f"duplicate ids: {dupes}")

    print(f"corpus entries (jsonl): {total}")
    print(f"by difficulty:          {dict(by_difficulty)}")
    print(f"negative cases:         {negative} "
          f"({negative * 100 // max(total, 1)}% expect unknown/refuse/defer/sanitise)")

    if negative * 4 < total:
        errors.append(
            "fewer than 25% of cases have a negative expectation — the corpus is "
            "too easy to prove anything about failure handling"
        )

    print()
    if errors:
        for e in errors:
            print(f"[FAIL] {e}")
        return 1
    print("[PASS] corpus is well-formed, ids unique, and contains real negative cases")
    return 0


if __name__ == "__main__":
    sys.exit(main())
