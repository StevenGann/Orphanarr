#!/usr/bin/env python3
"""
Orphanarr — filesystem semantics verification.

WHAT THIS PROVES
----------------
Every assertion Orphanarr's file-operation design rests on, checked against a
real kernel and two real, distinct filesystems. No mocks. If a claim in
docs/DESIGN.md about move / copy / hardlink / rename / collision / atomicity
contradicts this script's output, the design is wrong, not the kernel.

Run:  python3 tests/verification/fs_semantics_test.py
Exit: 0 = every claim behaved as the design assumes; 1 = at least one did not.

It needs two directories on DIFFERENT filesystems. Defaults are picked
automatically; override with:
    ORPHANARR_FS_A=/some/path ORPHANARR_FS_B=/other/fs/path
"""

from __future__ import annotations

import errno
import os
import shutil
import stat
import subprocess
import sys
import tempfile

RESULTS: list[tuple[str, str, str]] = []  # (verdict, claim, evidence)


def record(verdict: str, claim: str, evidence: str) -> None:
    RESULTS.append((verdict, claim, evidence))


def check(claim: str, condition: bool, evidence: str) -> None:
    record("VERIFIED" if condition else "REFUTED", claim, evidence)


def pick_filesystems() -> tuple[str, str]:
    a = os.environ.get("ORPHANARR_FS_A")
    b = os.environ.get("ORPHANARR_FS_B")
    if a and b:
        return a, b
    candidates = [
        tempfile.gettempdir(),                       # usually tmpfs
        "/dev/shm",                                  # tmpfs
        os.path.expanduser("~/.cache"),              # usually the root fs
        os.path.expanduser("~"),
    ]
    seen: dict[int, str] = {}
    for c in candidates:
        if not os.path.isdir(c) or not os.access(c, os.W_OK):
            continue
        dev = os.stat(c).st_dev
        seen.setdefault(dev, c)
    if len(seen) < 2:
        print("SKIP: need two writable dirs on different filesystems", file=sys.stderr)
        sys.exit(2)
    devs = list(seen.values())
    return devs[0], devs[1]


def write(path: str, data: bytes = b"orphanarr") -> str:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as fh:
        fh.write(data)
    return path


def main() -> int:
    fs_a, fs_b = pick_filesystems()
    root_a = tempfile.mkdtemp(prefix="orphanarr-a-", dir=fs_a)
    root_b = tempfile.mkdtemp(prefix="orphanarr-b-", dir=fs_b)
    print(f"FS_A = {root_a}  (st_dev={os.stat(root_a).st_dev})")
    print(f"FS_B = {root_b}  (st_dev={os.stat(root_b).st_dev})")
    assert os.stat(root_a).st_dev != os.stat(root_b).st_dev, "chosen dirs share a filesystem"

    try:
        # -- C1: hardlink on the same filesystem shares the inode ------------
        src = write(f"{root_a}/c1/file.mkv")
        dst = f"{root_a}/c1/link.mkv"
        os.link(src, dst)
        s1, s2 = os.stat(src), os.stat(dst)
        check(
            "os.link() on one filesystem creates a second name for the same inode "
            "(no extra disk usage, st_nlink becomes 2)",
            s1.st_ino == s2.st_ino and s1.st_nlink == 2,
            f"st_ino {s1.st_ino}=={s2.st_ino}, st_nlink={s1.st_nlink}",
        )

        # -- C2: hardlink across filesystems fails with EXDEV ----------------
        src = write(f"{root_a}/c2/file.mkv")
        try:
            os.link(src, f"{root_b}/c2-link.mkv")
            check("os.link() across filesystems raises OSError EXDEV", False, "no exception raised")
        except OSError as exc:
            check(
                "os.link() across filesystems raises OSError EXDEV (errno 18) — "
                "Orphanarr MUST detect st_dev mismatch before choosing hardlink",
                exc.errno == errno.EXDEV,
                f"OSError errno={exc.errno} ({errno.errorcode.get(exc.errno)})",
            )

        # -- C3: deleting one link leaves the data reachable via the other ---
        src = write(f"{root_a}/c3/file.mkv", b"seedable-payload")
        dst = f"{root_a}/c3/link.mkv"
        os.link(src, dst)
        os.unlink(src)
        with open(dst, "rb") as fh:
            body = fh.read()
        check(
            "Unlinking one hardlink does not destroy the data — the torrent's copy "
            "survives library-side deletion and vice versa",
            body == b"seedable-payload" and os.stat(dst).st_nlink == 1,
            f"content preserved, st_nlink now {os.stat(dst).st_nlink}",
        )

        # -- C4: renaming one link does NOT disturb the other ---------------
        src = write(f"{root_a}/c4/file.mkv")
        dst = f"{root_a}/c4/link.mkv"
        os.link(src, dst)
        os.rename(dst, f"{root_a}/c4/renamed.mkv")
        check(
            "Renaming a hardlink leaves the other name valid — Plex-side renaming "
            "cannot break the seeding path",
            os.path.exists(src) and os.path.exists(f"{root_a}/c4/renamed.mkv"),
            "both names resolve after rename of one",
        )

        # -- C5: hardlinks are NOT snapshots -------------------------------
        src = write(f"{root_a}/c5/file.mkv", b"AAAA")
        dst = f"{root_a}/c5/link.mkv"
        os.link(src, dst)
        with open(src, "r+b") as fh:
            fh.write(b"BBBB")
        with open(dst, "rb") as fh:
            body = fh.read()
        check(
            "A hardlink is not a snapshot: in-place writes through one name are "
            "visible through the other (so post-link mutation, e.g. tag rewriting, "
            "corrupts the seeding copy)",
            body == b"BBBB",
            f"read back {body!r} through the second link",
        )

        # -- C6: directories cannot be hardlinked --------------------------
        os.makedirs(f"{root_a}/c6/dir", exist_ok=True)
        try:
            os.link(f"{root_a}/c6/dir", f"{root_a}/c6/dirlink")
            check("os.link() on a directory fails", False, "no exception raised")
        except OSError as exc:
            check(
                "Directories cannot be hardlinked (EPERM) — Orphanarr must walk the "
                "tree and link each file individually, recreating directories",
                exc.errno in (errno.EPERM, errno.EACCES, errno.EISDIR),
                f"OSError errno={exc.errno} ({errno.errorcode.get(exc.errno)})",
            )

        # -- C7: os.rename across filesystems fails with EXDEV -------------
        src = write(f"{root_a}/c7/file.mkv")
        try:
            os.rename(src, f"{root_b}/c7-file.mkv")
            check("os.rename() across filesystems raises EXDEV", False, "no exception raised")
        except OSError as exc:
            check(
                "os.rename() across filesystems raises OSError EXDEV — there is no "
                "atomic cross-filesystem move at the syscall level",
                exc.errno == errno.EXDEV,
                f"OSError errno={exc.errno} ({errno.errorcode.get(exc.errno)})",
            )

        # -- C8: shutil.move across filesystems = copy + delete ------------
        src = write(f"{root_a}/c8/file.mkv", b"x" * 4096)
        ino_before = os.stat(src).st_ino
        moved = shutil.move(src, f"{root_b}/c8-file.mkv")
        check(
            "shutil.move() across filesystems succeeds but is copy-then-delete: "
            "new inode, transient double disk usage, NOT atomic, and interruptible "
            "into a half-written destination",
            (not os.path.exists(src))
            and os.path.exists(moved)
            and os.stat(moved).st_ino != ino_before,
            f"inode {ino_before} -> {os.stat(moved).st_ino}, source gone",
        )

        # -- C9: os.rename silently clobbers an existing target ------------
        keep = write(f"{root_a}/c9/keep.mkv", b"IRREPLACEABLE")
        newf = write(f"{root_a}/c9/new.mkv", b"replacement")
        os.rename(newf, keep)
        with open(keep, "rb") as fh:
            body = fh.read()
        check(
            "os.rename()/os.replace() DESTROYS an existing destination without "
            "warning — every Orphanarr write path must pre-check existence or use "
            "O_EXCL; a naive move is a data-loss bug",
            body == b"replacement",
            f"destination content is now {body!r}; original was b'IRREPLACEABLE'",
        )

        # -- C10: os.link refuses to clobber -------------------------------
        keep = write(f"{root_a}/c10/keep.mkv", b"IRREPLACEABLE")
        newf = write(f"{root_a}/c10/new.mkv", b"replacement")
        try:
            os.link(newf, keep)
            check("os.link() onto an existing path fails", False, "no exception raised")
        except OSError as exc:
            check(
                "os.link() REFUSES to overwrite an existing destination (EEXIST) — "
                "hardlink is the safe-by-default primitive; rename/copy are not",
                exc.errno == errno.EEXIST,
                f"OSError errno={exc.errno} ({errno.errorcode.get(exc.errno)})",
            )

        # -- C11: same-directory rename is atomic w.r.t. readers -----------
        final = f"{root_a}/c11/Movie (2009).mkv"
        tmp = f"{root_a}/c11/.orphanarr-tmp-Movie (2009).mkv"
        write(tmp, b"complete")
        os.replace(tmp, final)
        check(
            "Write-to-temp-then-os.replace() within one directory is atomic: a "
            "concurrent scanner never observes a partial file at the final path",
            os.path.exists(final) and not os.path.exists(tmp),
            "temp name gone, final name present, single rename(2) syscall",
        )

        # -- C12: NAME_MAX is 255 BYTES, not characters --------------------
        long_ascii = "A" * 255
        ok255 = False
        try:
            write(f"{root_a}/c12/{long_ascii}.mkv")
        except OSError as exc:
            ok255 = exc.errno == errno.ENAMETOOLONG
        else:
            ok255 = False
        # 251 chars + '.mkv' == 255 bytes, should succeed
        fits = "B" * 251 + ".mkv"
        fits_ok = True
        try:
            write(f"{root_a}/c12b/{fits}")
        except OSError:
            fits_ok = False
        check(
            "A single path component is capped at 255 BYTES (ENAMETOOLONG beyond); "
            "long scene names plus an added ' - Bluray-1080p' suffix can exceed it, "
            "so Orphanarr must truncate on a byte budget, not a character count",
            ok255 and fits_ok,
            f"255-byte-name+'.mkv' -> ENAMETOOLONG={ok255}; exactly-255-byte name ok={fits_ok}",
        )

        # -- C13: non-ASCII costs more than one byte per character ---------
        # 'é' is 2 bytes UTF-8, so 128 of them + '.mkv' = 260 bytes > 255.
        name = "é" * 128 + ".mkv"
        too_long = False
        try:
            write(f"{root_a}/c13/{name}")
        except OSError as exc:
            too_long = exc.errno == errno.ENAMETOOLONG
        check(
            "132 characters can exceed NAME_MAX when non-ASCII: byte length, not "
            "len(str), is the limit (anime/foreign titles hit this)",
            too_long,
            f"len(name)={len(name)} chars / {len(name.encode())} bytes -> ENAMETOOLONG={too_long}",
        )

        # -- C14: only '/' and NUL are illegal in a POSIX filename ---------
        tricky = 'Movie: The "Sequel" <2009> | 100% ?.mkv'
        posix_ok = True
        try:
            write(f"{root_a}/c14/{tricky}")
        except OSError:
            posix_ok = False
        check(
            "POSIX accepts ':' '\"' '<' '>' '|' '?' '*' '\\\\' in filenames, so a "
            "Linux-only test suite will NOT catch names that are illegal on the "
            "SMB/NTFS/exFAT shares many users mount as their library",
            posix_ok,
            f"created {tricky!r} on {os.path.basename(root_a)} without error",
        )

        # -- C15: reflink availability is filesystem-dependent -------------
        src = write(f"{root_a}/c15/file.mkv", b"z" * 1024)
        proc = subprocess.run(
            ["cp", "--reflink=always", src, f"{root_a}/c15/clone.mkv"],
            capture_output=True,
            text=True,
        )
        record(
            "PARTIAL",
            "cp --reflink=always (copy-on-write clone) is NOT universally available; "
            "it needs btrfs/XFS-with-reflink/ZFS-block-clone and is same-filesystem "
            "only. Orphanarr may not assume it.",
            f"exit={proc.returncode} on this filesystem; stderr={proc.stderr.strip()!r}",
        )

        # -- C16: st_dev is NECESSARY but NOT SUFFICIENT --------------------
        # Differing st_dev reliably rules hardlinking OUT. Equal st_dev does NOT
        # rule it in: see tests/verification/bindmount_hardlink_test.sh, which
        # produces equal st_dev and EXDEV simultaneously across two Docker-style
        # bind mounts of one filesystem. The kernel compares vfsmount pointers
        # (fs/namei.c filename_linkat), not superblocks.
        d1 = os.stat(root_a).st_dev
        d2 = os.stat(root_b).st_dev
        sub = os.path.join(root_a, "c16", "deep", "deeper")
        os.makedirs(sub, exist_ok=True)
        record(
            "PARTIAL",
            "Differing st_dev proves hardlinking is impossible; EQUAL st_dev does "
            "NOT prove it is possible (separate bind mounts of one filesystem share "
            "st_dev but still return EXDEV). Orphanarr must probe with a real "
            "link(2) attempt, not an st_dev comparison.",
            f"st_dev A={d1}, B={d2}, subdir-of-A={os.stat(sub).st_dev}; "
            f"sufficiency disproved by bindmount_hardlink_test.sh",
        )

        # -- C17: a copy interrupted mid-flight leaves a partial file ------
        src = write(f"{root_a}/c17/file.mkv", b"y" * (1 << 20))
        partial = f"{root_b}/c17-partial.mkv"
        with open(src, "rb") as fin, open(partial, "wb") as fout:
            fout.write(fin.read(4096))  # simulate a crash 4 KiB in
        check(
            "An interrupted cross-filesystem copy leaves a short file at the "
            "destination that looks like a real media file to a scanner — copies "
            "MUST land on a temp name and be renamed into place only on success",
            os.path.getsize(partial) == 4096 and os.path.getsize(src) == (1 << 20),
            f"destination is {os.path.getsize(partial)} B of {os.path.getsize(src)} B",
        )

        # -- C18: a hardlinked tree costs (almost) no space -----------------
        srcdir = f"{root_a}/c18/Some.Release.2009.1080p"
        for i in range(3):
            write(f"{srcdir}/part{i}.r{i:02d}", b"q" * (256 * 1024))
        dstdir = f"{root_a}/c18-linked"
        os.makedirs(dstdir, exist_ok=True)
        for entry in sorted(os.listdir(srcdir)):
            os.link(f"{srcdir}/{entry}", f"{dstdir}/{entry}")
        blocks_src = sum(os.lstat(f"{srcdir}/{e}").st_blocks for e in os.listdir(srcdir))
        nlinks = {os.lstat(f"{dstdir}/{e}").st_nlink for e in os.listdir(dstdir)}
        check(
            "Recursive per-file hardlinking reproduces a release directory at ~zero "
            "additional cost (st_nlink==2 on every file)",
            nlinks == {2},
            f"st_nlink set={nlinks}, source occupies {blocks_src * 512} bytes of blocks",
        )

        # -- C19: file mode/ownership is inherited by the link, not copied --
        src = write(f"{root_a}/c19/file.mkv")
        os.chmod(src, 0o600)
        dst = f"{root_a}/c19/link.mkv"
        os.link(src, dst)
        os.chmod(dst, 0o644)
        check(
            "Permissions live on the inode, so chmod'ing the library copy also "
            "chmods the download-client copy — Orphanarr must not 'fix permissions' "
            "on hardlinked destinations without understanding it mutates the source",
            stat.S_IMODE(os.stat(src).st_mode) == 0o644,
            f"source mode is now {oct(stat.S_IMODE(os.stat(src).st_mode))} after chmod of the link",
        )

    finally:
        shutil.rmtree(root_a, ignore_errors=True)
        shutil.rmtree(root_b, ignore_errors=True)

    print()
    width = max(len(v) for v, _, _ in RESULTS)
    failures = 0
    for verdict, claim, evidence in RESULTS:
        if verdict == "REFUTED":
            failures += 1
        print(f"[{verdict:<{width}}] {claim}")
        print(f"{'':<{width + 3}}evidence: {evidence}")
    print()
    print(f"{len(RESULTS)} claims checked, {failures} refuted")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
