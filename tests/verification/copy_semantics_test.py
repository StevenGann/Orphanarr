#!/usr/bin/env python3
"""
Orphanarr — COPY-path filesystem semantics verification.

WHY THIS FILE EXISTS
--------------------
`fs_semantics_test.py` (#C1-#C19) was written when the design was hardlink-first;
more than half of its claims are about hardlink behaviour. The stakeholder's
answer to BRIEF Q1 on 2026-08-06 — *"Let's assume a full copy for now"* — makes
the copy path the ONLY path. Every assertion the copy path rests on is therefore
load-bearing, and most of them were never checked.

This script checks them, against a real kernel and real filesystems. Where a
claim needs a filesystem that can run out of space, it re-executes itself inside
an unprivileged user+mount namespace (`unshare -Urm`) and mounts a 1 MiB tmpfs —
the same technique `bindmount_hardlink_test.sh` uses, no Docker and no root.

Numbering continues #C19 -> #C20 so a citation like `#C21` is unambiguous across
both files.

Run:  python3 tests/verification/copy_semantics_test.py
Exit: 0 = every claim behaved as the design assumes; 1 = at least one did not.

Override filesystem selection with ORPHANARR_FS_A / ORPHANARR_FS_B.
"""

from __future__ import annotations

import ctypes
import errno
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile

RESULTS: list[tuple[str, str, str]] = []  # (verdict, claim, evidence)

PARTIAL_SUFFIX = ".orphanarr-partial"  # DESIGN.md §6.5

libc = ctypes.CDLL(None, use_errno=True)


def record(verdict: str, claim: str, evidence: str) -> None:
    RESULTS.append((verdict, claim, evidence))


def check(claim: str, condition: bool, evidence: str) -> None:
    record("VERIFIED" if condition else "REFUTED", claim, evidence)


def pick_filesystems() -> tuple[str, str]:
    a = os.environ.get("ORPHANARR_FS_A")
    b = os.environ.get("ORPHANARR_FS_B")
    if a and b:
        return a, b
    # Ordered so the DESTINATION lands on a real disk filesystem where possible:
    # #C28 (root-reserved blocks) and #C31 (sparse inflation) are only
    # informative on a filesystem that has those properties.
    candidates = [
        tempfile.gettempdir(),
        os.path.expanduser("~/.cache"),
        "/dev/shm",
        os.path.expanduser("~"),
    ]
    seen: dict[int, str] = {}
    for c in candidates:
        if not os.path.isdir(c) or not os.access(c, os.W_OK):
            continue
        seen.setdefault(os.stat(c).st_dev, c)
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


def naive_copy(src: str, dst: str, chunk: int = 1 << 16) -> int:
    """The copy every implementation writes. Deliberately unclever."""
    n = 0
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    with open(src, "rb") as fin, open(dst, "wb") as fout:
        while True:
            buf = fin.read(chunk)
            if not buf:
                break
            fout.write(buf)
            n += len(buf)
        fout.flush()
        os.fsync(fout.fileno())
    return n


# --------------------------------------------------------------------------
# ENOSPC child — runs inside `unshare -Urm` against a 1 MiB tmpfs.
# --------------------------------------------------------------------------

MS_NOSUID = 2


def enospc_child() -> int:
    out: dict[str, object] = {}
    mnt = tempfile.mkdtemp(prefix="orphanarr-enospc-")
    rc = libc.mount(b"tmpfs", mnt.encode(), b"tmpfs", MS_NOSUID, b"size=1048576")
    if rc != 0:
        out["error"] = f"mount failed errno={ctypes.get_errno()}"
        print(json.dumps(out))
        return 3

    src = tempfile.mktemp(prefix="orphanarr-enospc-src-")
    with open(src, "wb") as fh:
        fh.write(b"M" * (4 << 20))  # 4 MiB source into a 1 MiB destination

    dst = os.path.join(mnt, "Movie (2009).mkv" + PARTIAL_SUFFIX)
    st = os.statvfs(mnt)
    out["free_before"] = st.f_bavail * st.f_frsize

    stage = "write"
    written = 0
    try:
        with open(src, "rb") as fin, open(dst, "wb") as fout:
            while True:
                buf = fin.read(1 << 16)
                if not buf:
                    break
                fout.write(buf)
                written += len(buf)
            stage = "fsync"
            fout.flush()
            os.fsync(fout.fileno())
        out["errno"] = 0
    except OSError as exc:
        out["errno"] = exc.errno
        out["errname"] = errno.errorcode.get(exc.errno, "?")
        out["stage"] = stage
    out["written_before_error"] = written
    out["partial_size"] = os.path.getsize(dst) if os.path.exists(dst) else -1
    st = os.statvfs(mnt)
    out["free_at_enospc"] = st.f_bavail * st.f_frsize

    # The design's rule: unlink the partial on any exit that is not a
    # successful publish. Does that actually give the space back?
    if os.path.exists(dst):
        os.unlink(dst)
    st = os.statvfs(mnt)
    out["free_after_unlink"] = st.f_bavail * st.f_frsize
    out["dir_after_unlink"] = os.listdir(mnt)

    os.unlink(src)
    print(json.dumps(out))
    return 0


def run_enospc_probe() -> dict[str, object] | None:
    if subprocess.run(
        ["unshare", "-Urm", "--propagation", "private", "true"],
        capture_output=True,
    ).returncode != 0:
        return None
    proc = subprocess.run(
        ["unshare", "-Urm", "--propagation", "private",
         sys.executable, os.path.abspath(__file__), "--enospc-child"],
        capture_output=True, text=True,
    )
    if proc.returncode != 0 or not proc.stdout.strip():
        return None
    try:
        return json.loads(proc.stdout.strip().splitlines()[-1])
    except json.JSONDecodeError:
        return None


# --------------------------------------------------------------------------


def main() -> int:
    fs_a, fs_b = pick_filesystems()
    root_a = tempfile.mkdtemp(prefix="orphanarr-src-", dir=fs_a)   # "downloads"
    root_b = tempfile.mkdtemp(prefix="orphanarr-lib-", dir=fs_b)   # "library"
    print(f"SOURCE  (downloads) = {root_a}  (st_dev={os.stat(root_a).st_dev})")
    print(f"DEST    (library)   = {root_b}  (st_dev={os.stat(root_b).st_dev})")
    assert os.stat(root_a).st_dev != os.stat(root_b).st_dev

    try:
        # -- C20: a copy is a fresh inode ---------------------------------
        # BRIEF §5 A1 asserts this as the premise for "chmod/chown is now safe".
        src = write(f"{root_a}/c20/file.mkv", b"payload")
        os.chmod(src, 0o600)
        dst = f"{root_b}/c20/file.mkv"
        naive_copy(src, dst)
        s_src, s_dst = os.stat(src), os.stat(dst)
        check(
            "A copy is a FRESH INODE with no relationship to the source: different "
            "st_ino, st_nlink==1 on both. This is the premise BRIEF §5 A1 rests on.",
            s_src.st_ino != s_dst.st_ino and s_dst.st_nlink == 1 and s_src.st_nlink == 1,
            f"src ino={s_src.st_ino} nlink={s_src.st_nlink}; "
            f"dst ino={s_dst.st_ino} nlink={s_dst.st_nlink}",
        )

        # -- C21: chmod on a copy does NOT reach the source ----------------
        # The exact converse of #C19, which is what made chmod forbidden.
        os.chmod(dst, 0o664)
        check(
            "chmod on a COPIED destination leaves the source's mode untouched — the "
            "prohibition #C19 established applies to hardlinks ONLY. Under copy-only, "
            "setting modes on output is safe AND is the fix for §6.4's #1 silent failure.",
            stat.S_IMODE(os.stat(src).st_mode) == 0o600
            and stat.S_IMODE(os.stat(dst).st_mode) == 0o664,
            f"src mode {oct(stat.S_IMODE(os.stat(src).st_mode))} (unchanged), "
            f"dst mode {oct(stat.S_IMODE(os.stat(dst).st_mode))}",
        )

        # -- C22: the copy is owned by the COPYING PROCESS -----------------
        check(
            "A copy is owned by the euid/egid of the process that made it, not by the "
            "source's owner. So the library file is readable by the media server iff "
            "Orphanarr's own uid/gid and mode make it so — ownership is not inherited "
            "from qBittorrent, and that is the whole reason A1 fixes §6.4.",
            os.stat(dst).st_uid == os.geteuid() and os.stat(dst).st_gid == os.getegid(),
            f"dst uid/gid = {os.stat(dst).st_uid}/{os.stat(dst).st_gid}; "
            f"process euid/egid = {os.geteuid()}/{os.getegid()}",
        )

        # -- C23: chown to another uid needs privilege ---------------------
        if os.geteuid() == 0:
            record(
                "UNVERIFIABLE",
                "An unprivileged process cannot chown() a file to another uid",
                "test host is running as root; rerun unprivileged to check this",
            )
        else:
            target_uid = 0 if os.geteuid() != 0 else 1
            try:
                os.chown(dst, target_uid, -1)
                check(
                    "An unprivileged process cannot chown() to another uid",
                    False,
                    "chown to uid 0 unexpectedly succeeded",
                )
            except OSError as exc:
                check(
                    "chown() to a different uid fails with EPERM without CAP_CHOWN. "
                    "So 'Orphanarr should set ownership on its output' (BRIEF §5 A1 / "
                    "Q25) is only available when the container runs as root — under "
                    "PUID/PGID after su-exec, or `user: 1000:1000`, it is NOT. The "
                    "portable lever is MODE, not owner.",
                    exc.errno in (errno.EPERM, errno.EACCES),
                    f"os.chown(dst, {target_uid}, -1) -> errno={exc.errno} "
                    f"({errno.errorcode.get(exc.errno)})",
                )
            # chgrp to a supplementary group we belong to IS permitted.
            others = [g for g in os.getgroups() if g != os.getegid()]
            if others:
                try:
                    os.chown(dst, -1, others[0])
                    ok = os.stat(dst).st_gid == others[0]
                except OSError:
                    ok = False
                record(
                    "PARTIAL" if ok else "REFUTED",
                    "chgrp to a supplementary group the process already belongs to IS "
                    "permitted unprivileged — so 'run Orphanarr with the media server's "
                    "group as a supplementary group' is a working, privilege-free "
                    "remediation where chown is not.",
                    f"chgrp to gid {others[0]} succeeded={ok}",
                )
                os.chown(dst, -1, os.getegid())

        # -- C24: umask silently strips bits from a created file -----------
        old = os.umask(0o077)
        try:
            p = f"{root_b}/c24/file.mkv"
            os.makedirs(os.path.dirname(p), exist_ok=True)
            fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o666)
            os.close(fd)
            created = stat.S_IMODE(os.stat(p).st_mode)
        finally:
            os.umask(old)
        check(
            "umask silently strips permission bits at creation: open(..., 0o666) under "
            "umask 077 yields 0600. A copy-only Orphanarr that relies on the requested "
            "mode ships a library only its own uid can read — the same invisible failure "
            "§6.4 describes, now caused by us instead of by qBittorrent. Modes must be "
            "set with an explicit chmod, not passed to open().",
            created == 0o600,
            f"open(mode=0o666) under umask 0o077 produced {oct(created)}",
        )

        # -- C25: a setgid destination directory sets the group, no privilege
        others = [g for g in os.getgroups() if g != os.getegid()]
        if not others:
            record(
                "UNVERIFIABLE",
                "A set-gid destination directory gives fresh files that directory's gid",
                "process belongs to no supplementary group; cannot construct the case",
            )
        else:
            gd = f"{root_b}/c25"
            os.makedirs(gd, exist_ok=True)
            os.chown(gd, -1, others[0])
            os.chmod(gd, 0o2775)
            p = f"{gd}/file.mkv"
            naive_copy(src, p)
            check(
                "A set-gid (g+s) destination directory makes every fresh file inherit "
                "the DIRECTORY's group, with no privilege at all. This is the "
                "privilege-free version of the §6.4 fix: `chmod g+s` the library root, "
                "0664/0775 modes, and the media server's group reads everything — no "
                "chown, no root, no CAP_CHOWN.",
                os.stat(p).st_gid == others[0],
                f"dir gid={others[0]} setgid=on -> new file gid={os.stat(p).st_gid} "
                f"(process egid={os.getegid()})",
            )

        # -- C26: mode set on the partial survives the link+unlink publish --
        d = f"{root_b}/c26"
        os.makedirs(d, exist_ok=True)
        partial = f"{d}/Movie (2009).mkv{PARTIAL_SUFFIX}"
        final = f"{d}/Movie (2009).mkv"
        naive_copy(src, partial)
        os.chmod(partial, 0o664)
        os.link(partial, final)      # §6.5 publish step 1
        os.unlink(partial)           # §6.5 publish step 1
        st_f = os.stat(final)
        check(
            "Mode set on the .orphanarr-partial survives the link+unlink publish "
            "(mode lives on the inode, and publish creates a name, not an inode), and "
            "the published file ends at st_nlink==1. So chmod BEFORE publish: the file "
            "is never visible to a scanner with the wrong mode, and there is no window.",
            stat.S_IMODE(st_f.st_mode) == 0o664 and st_f.st_nlink == 1,
            f"published mode={oct(stat.S_IMODE(st_f.st_mode))}, st_nlink={st_f.st_nlink}",
        )

        # -- C27: renameat2(RENAME_NOREPLACE) refuses to clobber ------------
        # DESIGN §6.5 publish fallback 2. Never checked until now.
        d = f"{root_b}/c27"
        os.makedirs(d, exist_ok=True)
        write(f"{d}/keep.mkv", b"IRREPLACEABLE")
        write(f"{d}/new.mkv", b"replacement")
        SYS_renameat2 = {"x86_64": 316, "aarch64": 276}.get(os.uname().machine)
        if SYS_renameat2 is None:
            record(
                "UNVERIFIABLE",
                "renameat2(RENAME_NOREPLACE) refuses to overwrite an existing destination",
                f"no syscall number wired for machine={os.uname().machine}",
            )
        else:
            dirfd = os.open(d, os.O_RDONLY | os.O_DIRECTORY)
            ctypes.set_errno(0)
            rc = libc.syscall(
                SYS_renameat2,
                ctypes.c_int(dirfd), b"new.mkv",
                ctypes.c_int(dirfd), b"keep.mkv",
                ctypes.c_uint(1),  # RENAME_NOREPLACE
            )
            en = ctypes.get_errno()
            os.close(dirfd)
            with open(f"{d}/keep.mkv", "rb") as fh:
                body = fh.read()
            check(
                "renameat2(RENAME_NOREPLACE) returns EEXIST and leaves the existing "
                "destination byte-identical — §6.5's publish fallback #2 works on this "
                "kernel and filesystem, and unlike plain rename(2) (#C9) it cannot "
                "destroy a file the user already had.",
                rc == -1 and en == errno.EEXIST and body == b"IRREPLACEABLE",
                f"rc={rc} errno={en} ({errno.errorcode.get(en)}); "
                f"destination still {body!r}",
            )

        # -- C28: f_bavail is not f_bfree -----------------------------------
        stv = os.statvfs(root_b)
        reserve = (stv.f_bfree - stv.f_bavail) * stv.f_frsize
        record(
            "VERIFIED" if reserve > 0 else "PARTIAL",
            "Free-space preflight must use f_bavail (space available to an "
            "unprivileged process), NOT f_bfree. ext4 reserves 5% for root by "
            "default, so a preflight on f_bfree passes and the copy then hits ENOSPC. "
            "Go's syscall.Statfs_t exposes both fields one line apart with "
            "near-identical names.",
            f"{root_b}: f_bfree={stv.f_bfree} f_bavail={stv.f_bavail} "
            f"frsize={stv.f_frsize} -> root-reserved {reserve} bytes "
            f"({reserve / (1 << 30):.2f} GiB) invisible to an unprivileged writer",
        )

        # -- C29: a source mutated mid-copy passes size verification --------
        d = f"{root_a}/c29"
        os.makedirs(d, exist_ok=True)
        msrc = f"{d}/file.mkv"
        half = 512 * 1024
        with open(msrc, "wb") as fh:
            fh.write(b"A" * half + b"B" * half)
        before = os.stat(msrc)
        mdst = f"{root_b}/c29-file.mkv"
        with open(msrc, "rb") as fin, open(mdst, "wb") as fout:
            fout.write(fin.read(half))                      # copied "A"
            with open(msrc, "r+b") as mutator:               # qBittorrent rewrites
                mutator.seek(0)                              # a failed-hash piece,
                mutator.write(b"C" * half)                   # or a cross-seed torrent
                mutator.seek(half)                           # writes the same files
                mutator.write(b"D" * half)
            fout.write(fin.read())                          # copies "D"
            fout.flush()
            os.fsync(fout.fileno())
        after = os.stat(msrc)
        with open(mdst, "rb") as fh:
            got = fh.read()
        with open(msrc, "rb") as fh:
            now = fh.read()
        size_check_passes = os.path.getsize(mdst) == after.st_size
        frankenstein = got != now and got != (b"A" * half + b"B" * half)
        check(
            "A source mutated WHILE it is being copied yields a destination that "
            "matches neither the source's old contents nor its new contents, and that "
            "PASSES §6.5's size verification. This failure mode did not exist under "
            "hardlinking (one inode, nothing to tear) and is now on the only path. "
            "st_mtime_ns and st_size taken before and after the copy detect it for "
            "free; a size check alone cannot.",
            size_check_passes and frankenstein
            and before.st_mtime_ns != after.st_mtime_ns,
            f"dst[0:1]={got[:1]!r} dst[{half}:{half+1}]={got[half:half+1]!r} "
            f"src_now[0:1]={now[:1]!r}; sizes equal={size_check_passes}; "
            f"mtime_ns {before.st_mtime_ns} -> {after.st_mtime_ns}",
        )

        # -- C30: the 237-byte budget (§5.8) --------------------------------
        d = f"{root_b}/c30"
        os.makedirs(d, exist_ok=True)
        suffix_len = len(PARTIAL_SUFFIX.encode())
        name255 = "N" * (255 - 4) + ".mkv"
        assert len(name255.encode()) == 255
        over = False
        try:
            fd = os.open(f"{d}/{name255}{PARTIAL_SUFFIX}", os.O_WRONLY | os.O_CREAT, 0o644)
            os.close(fd)
        except OSError as exc:
            over = exc.errno == errno.ENAMETOOLONG
        budget = 255 - suffix_len
        name_budget = "N" * (budget - 4) + ".mkv"
        assert len(name_budget.encode()) == budget
        fits = True
        try:
            fd = os.open(f"{d}/{name_budget}{PARTIAL_SUFFIX}", os.O_WRONLY | os.O_CREAT, 0o644)
            os.close(fd)
        except OSError:
            fits = False
        check(
            "§5.8's copy-path name budget is exactly 237 bytes and the arithmetic is "
            "right: len('.orphanarr-partial') == 18, a 255-byte destination name plus "
            "the suffix is ENAMETOOLONG, and a 237-byte name plus the suffix is exactly "
            "255 and succeeds. Under copy-only EVERY placement goes through the partial, "
            "so this budget is no longer a fallback-path rule — it is THE rule.",
            over and fits and suffix_len == 18 and budget == 237,
            f"suffix={suffix_len}B, budget={budget}B; 255B name + suffix -> "
            f"ENAMETOOLONG={over}; {budget}B name + suffix -> created={fits}",
        )

        # -- C31: a naive copy inflates a sparse source ---------------------
        d = f"{root_a}/c31"
        os.makedirs(d, exist_ok=True)
        sp = f"{d}/sparse.bin"
        with open(sp, "wb") as fh:
            fh.truncate(8 << 20)          # 8 MiB apparent
            fh.seek(8 << 20)
            fh.write(b"x")
        s_before = os.lstat(sp)
        spd = f"{root_b}/c31-sparse.bin"
        naive_copy(sp, spd)
        s_after = os.lstat(spd)
        check(
            "A naive read/write copy does NOT preserve holes: an 8 MiB sparse source "
            "occupying almost nothing on disk becomes 8 MiB of real blocks at the "
            "destination. Free-space preflight must therefore budget st_size (apparent), "
            "never st_blocks*512 (allocated) — the two differ by orders of magnitude on "
            "exactly the files a download client preallocates.",
            s_after.st_blocks * 512 > s_before.st_blocks * 512 * 4,
            f"src apparent={s_before.st_size} allocated={s_before.st_blocks * 512}; "
            f"dst apparent={s_after.st_size} allocated={s_after.st_blocks * 512}",
        )

        # -- C32: copy_file_range across two filesystems --------------------
        d = f"{root_a}/c32"
        os.makedirs(d, exist_ok=True)
        cs = write(f"{d}/file.mkv", b"k" * (1 << 20))
        cd = f"{root_b}/c32-file.mkv"
        fin = os.open(cs, os.O_RDONLY)
        fout = os.open(cd, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
        try:
            n = os.copy_file_range(fin, fout, 1 << 20)
            cfr = f"copied {n} bytes"
            cfr_ok = n > 0
            cfr_err = 0
        except OSError as exc:
            cfr = f"errno={exc.errno} ({errno.errorcode.get(exc.errno)})"
            cfr_ok = False
            cfr_err = exc.errno
        finally:
            os.close(fin)
            os.close(fout)
        record(
            "PARTIAL",
            "copy_file_range(2) across two DIFFERENT filesystems: behaviour is "
            "kernel- and filesystem-dependent (cross-fs support arrived in 5.3; it "
            "returns EXDEV where unsupported and has a history of short/zero returns "
            "on stacked filesystems). Go's io.Copy between two *os.File tries "
            "copy_file_range first, then sendfile, then a generic buffer — so the "
            "executor MUST handle a short return, and MUST NOT treat one call as the "
            "whole copy.",
            f"src_dev={os.stat(cs).st_dev} dst_dev={os.stat(cd).st_dev} "
            f"kernel={os.uname().release}: {cfr}; usable_here={cfr_ok}"
            + (f" errno_name={errno.errorcode.get(cfr_err)}" if cfr_err else ""),
        )

        # -- C33: ENOSPC mid-copy, on a filesystem that can actually fill ---
        probe = run_enospc_probe()
        if probe is None or "errno" not in probe:
            record(
                "UNVERIFIABLE",
                "ENOSPC mid-copy leaves a partial file, and unlinking it reclaims the space",
                "unprivileged user+mount namespace or tmpfs mount unavailable on this host",
            )
        else:
            en = probe["errno"]
            reclaimed = (
                isinstance(probe.get("free_after_unlink"), int)
                and probe["free_after_unlink"] >= probe["free_before"] * 0.9
            )
            check(
                "ENOSPC mid-copy: the write fails with errno 28, a large partial file "
                "is left occupying the destination filesystem, and unlinking it (the "
                "§6.5 step-5 rule) reclaims the space and leaves the library directory "
                "empty. Under copy-only this stops being an edge case and becomes the "
                "expected outcome of any plan the free-space preflight got wrong.",
                en == errno.ENOSPC and probe["partial_size"] > 0 and reclaimed
                and probe.get("dir_after_unlink") == [],
                f"errno={en} ({probe.get('errname')}) raised at {probe.get('stage')}(); "
                f"wrote {probe.get('written_before_error')} B into a "
                f"{probe.get('free_before')} B filesystem; partial left = "
                f"{probe.get('partial_size')} B; free {probe.get('free_at_enospc')} -> "
                f"{probe.get('free_after_unlink')} after unlink; "
                f"dir now {probe.get('dir_after_unlink')}",
            )

        # -- C34: reusing a leftover partial produces a stale tail ----------
        d = f"{root_b}/c34"
        os.makedirs(d, exist_ok=True)
        leftover = f"{d}/Movie (2009).mkv{PARTIAL_SUFFIX}"
        with open(leftover, "wb") as fh:
            fh.write(b"OLD-CRASHED-RUN" + b"Z" * (2048 - 15))
        excl_refused = False
        try:
            fd = os.open(leftover, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
            os.close(fd)
        except OSError as exc:
            excl_refused = exc.errno == errno.EEXIST
        # ... and what happens if you don't use O_EXCL or O_TRUNC:
        with open(leftover, "r+b") as fh:
            fh.write(b"N" * 1024)          # a shorter "new" copy over the old one
        body = open(leftover, "rb").read()
        stale_tail = len(body) == 2048 and body[:1024] == b"N" * 1024 and body[1024:] != b""
        check(
            "The partial file must be opened O_CREAT|O_EXCL. A leftover partial from a "
            "crashed run is refused (EEXIST), which is what routes it to Reconcile() "
            "instead of being silently reused. Opening it O_WRONLY without O_TRUNC "
            "writes the new copy over the head of the old one and leaves the old tail "
            "in place — a file that passes a size check with garbage at the end.",
            excl_refused and stale_tail,
            f"O_EXCL refused leftover: {excl_refused}; without O_TRUNC the file is "
            f"{len(body)} B = 1024 new + {len(body) - 1024} stale bytes "
            f"(tail starts {body[1024:1040]!r})",
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
    if "--enospc-child" in sys.argv:
        sys.exit(enospc_child())
    sys.exit(main())
