#!/usr/bin/env bash
# Orphanarr — read-only bind mount verification.
#
# WHAT THIS PROVES
# ----------------
# BRIEF §5 A3 (2026-08-06) recommends mounting every download folder `:ro` so
# that invariant I1 ("never modify, move, delete, rename, chmod, chown or
# truncate a source file") is enforced by the KERNEL rather than by the fsx.FS
# port or by developer discipline.
#
# That is a strong claim, and "read-only" is exactly the kind of word that gets
# believed without being checked. This script asks two questions:
#
#   1. Does a read-only bind mount actually refuse EVERY write path a Go program
#      could take — not just open(O_WRONLY)?
#   2. What does it NOT protect against?
#
# The answer to (2) is why this file exists. There are three holes, all of them
# reachable from configurations this project's own documentation recommends, and
# none of them is visible from inside the container.
#
# Technique is the same as bindmount_hardlink_test.sh: an unprivileged
# user+mount namespace (`unshare -Urm`). No Docker, no root.
#
# Run:  bash tests/verification/readonly_mount_test.sh
# Exit: 0 if reality matches the claims below.

set -u

if ! unshare -Urm --propagation private true 2>/dev/null; then
    echo "SKIP: unprivileged user+mount namespaces unavailable on this host" >&2
    exit 2
fi

# ~/.cache rather than /tmp so the backing filesystem supports user xattrs.
export ORPHANARR_RO_BASE="${ORPHANARR_RO_BASE:-$HOME/.cache}"

unshare -Urm --propagation private bash -s <<'INNER'
set -u
fail=0
say() { printf '%s\n' "$*"; }

ROOT=$(mktemp -d -p "$ORPHANARR_RO_BASE" orphanarr-ro-XXXXXX)

# The host-side layout: one filesystem, downloads and media beside each other.
mkdir -p "$ROOT/hostdata/torrents/complete/Some.Release.2009.1080p"
mkdir -p "$ROOT/hostdata/torrents/complete/nested-disk"
mkdir -p "$ROOT/hostdata/media/Movies"
echo -n "SEEDING-PAYLOAD" > "$ROOT/hostdata/torrents/complete/Some.Release.2009.1080p/movie.mkv"

# A symlink INSIDE the download tree pointing at the writable library. A user,
# a torrent, or an old *arr layout can leave one of these. It is created on the
# host side because that is the realistic origin.
ln -s "$ROOT/hostdata/media/escaped.mkv" "$ROOT/hostdata/torrents/complete/escape"

# A separate filesystem mounted UNDER the download root — the mergerfs branch /
# second-array-disk / unRAID-user-share shape, and a very common homelab layout.
mount -t tmpfs -o size=1M tmpfs "$ROOT/hostdata/torrents/complete/nested-disk"
echo -n "NESTED-PAYLOAD" > "$ROOT/hostdata/torrents/complete/nested-disk/movie2.mkv"

# ---------------------------------------------------------------------------
say "=== LAYOUT: the BRIEF §5 A3 recommendation ==="
say "    docker run -v /hostdata/torrents:/downloads:ro -v /hostdata/media:/media"
mkdir -p "$ROOT/ctr/downloads" "$ROOT/ctr/media" "$ROOT/ctrNR/downloads"
# Docker/runc mount `-v` bind mounts RECURSIVELY (MS_BIND|MS_REC) and then
# applies MS_RDONLY. Reproduce that, not a plain non-recursive bind.
mount --rbind "$ROOT/hostdata/torrents" "$ROOT/ctr/downloads"
mount -o remount,bind,ro "$ROOT/ctr/downloads"
mount --bind "$ROOT/hostdata/media" "$ROOT/ctr/media"
# And, for R5b, the NON-recursive variant beside it.
mount --bind "$ROOT/hostdata/torrents" "$ROOT/ctrNR/downloads"
say

python3 - "$ROOT" <<'PY'
import ctypes, errno, os, sys

root = sys.argv[1]
ro   = f"{root}/ctr/downloads"
f    = f"{ro}/complete/Some.Release.2009.1080p/movie.mkv"
rw   = f"{root}/ctr/media"
fail = 0
libc = ctypes.CDLL(None, use_errno=True)

def attempt(fn):
    try:
        fn()
        return None
    except OSError as exc:
        return exc.errno

cases = [
    ("open(O_WRONLY|O_CREAT) new file",  lambda: os.close(os.open(f"{ro}/new.mkv", os.O_WRONLY | os.O_CREAT, 0o644))),
    ("open(O_WRONLY) existing file",     lambda: os.close(os.open(f, os.O_WRONLY))),
    ("open(O_RDWR) existing file",       lambda: os.close(os.open(f, os.O_RDWR))),
    ("open(O_RDONLY|O_TRUNC)",           lambda: os.close(os.open(f, os.O_RDONLY | os.O_TRUNC))),
    ("truncate()",                       lambda: os.truncate(f, 0)),
    ("unlink()",                         lambda: os.unlink(f)),
    ("rename() within the mount",        lambda: os.rename(f, f"{ro}/renamed.mkv")),
    ("mkdir()",                          lambda: os.mkdir(f"{ro}/newdir")),
    ("rmdir()",                          lambda: os.rmdir(f"{ro}/complete/Some.Release.2009.1080p")),
    ("symlink() into the mount",         lambda: os.symlink("/etc/passwd", f"{ro}/l")),
    ("link() within the mount",          lambda: os.link(f, f"{ro}/hard.mkv")),
    ("chmod()",                          lambda: os.chmod(f, 0o777)),
    ("chown() to our OWN uid (no-op)",   lambda: os.chown(f, os.geteuid(), os.getegid())),
    ("utime()",                          lambda: os.utime(f, (0, 0))),
    ("setxattr()",                       lambda: os.setxattr(f, b"user.orphanarr", b"x")),
    ("mkfifo()",                         lambda: os.mkfifo(f"{ro}/fifo")),
]

def via_proc():
    fd = os.open(f, os.O_RDONLY)
    try:
        os.close(os.open(f"/proc/self/fd/{fd}", os.O_WRONLY))
    finally:
        os.close(fd)
cases.append(("re-open O_WRONLY via /proc/self/fd", via_proc))

def via_tmpfile():
    # O_TMPFILE creates an unnamed inode on the mount, which linkat(2) can then
    # give a name. It is a write to the filesystem that never names a path.
    fd = os.open(f"{ro}/complete", os.O_WRONLY | os.O_TMPFILE, 0o644)
    os.close(fd)
cases.append(("open(dir, O_TMPFILE|O_WRONLY)", via_tmpfile))

blocked, leaked, nonerofs = [], [], []
for name, fn in cases:
    en = attempt(fn)
    if en is None:
        leaked.append(name)
    else:
        blocked.append(name)
        if en != errno.EROFS:
            nonerofs.append(f"{name} -> {errno.errorcode.get(en, en)}")

say = print
say("=== R1: every write path a Go executor could take ===")
if not leaked:
    say(f"[VERIFIED] all {len(cases)} write attempts were refused by the read-only mount,")
    say(f"           including the two fd-laundering tricks. {len(cases) - len(nonerofs)} returned EROFS.")
    if nonerofs:
        say("           refused with a different errno (still refused): " + "; ".join(nonerofs))
else:
    say("[REFUTED ] these write paths SUCCEEDED through a read-only mount: " + ", ".join(leaked))
    fail = 1
say("           -> I1 is genuinely enforceable in the kernel for THIS mount.")
say("")

say("=== R2: can we still hardlink out of a read-only source mount? ===")
en = attempt(lambda: os.link(f, f"{rw}/Movies/linked.mkv"))
if en == errno.EXDEV:
    say("[VERIFIED] link() from the :ro download mount into the rw library mount fails")
    say("           with EXDEV. A read-only bind mount is a DISTINCT vfsmount, and")
    say("           fs/namei.c filename_linkat() compares vfsmount pointers — so")
    say("           mounting downloads :ro GUARANTEES copy-only, by construction.")
    say("           You cannot have both kernel-enforced I1 and opportunistic hardlinks.")
elif en is None:
    say("[REFUTED ] link() out of the read-only mount SUCCEEDED; :ro and hardlinking are")
    say("           not mutually exclusive on this kernel.")
    fail = 1
else:
    say(f"[PARTIAL ] link() failed with {errno.errorcode.get(en, en)}, not EXDEV — still no hardlink,")
    say("           but the mechanism differs from the one claimed.")
say("")

say("=== R3: a symlink inside the read-only tree pointing at a writable path ===")
try:
    with open(f"{ro}/complete/escape", "wb") as fh:
        fh.write(b"WROTE-THROUGH-A-SYMLINK")
    ok = open(f"{rw}/escaped.mkv", "rb").read()
    say("[VERIFIED] A symlink inside the :ro source tree resolves out of it, and the write")
    say(f"           lands on the writable side: {ok!r}")
    say("           -> the mount is read-only; the PATH is not. §6.2's 'symlinks are")
    say("              refused, not followed' rule is NOT made redundant by :ro.")
except OSError as exc:
    say(f"[REFUTED ] writing through the escaping symlink failed: {errno.errorcode.get(exc.errno, exc.errno)}")
    say("           (if this ever becomes the behaviour, record which kernel changed it)")
    fail = 1

sys.exit(fail)
PY
[ $? -ne 0 ] && fail=1
say

# --- R4: is `:ro` recursive over an rbind? --------------------------------
say "=== R4: does the read-only flag reach a nested submount (rbind, as Docker does)? ==="
if echo -n "MUTATED" > "$ROOT/ctr/downloads/complete/nested-disk/movie2.mkv" 2>/dev/null; then
    say "[VERIFIED] A nested submount under the bind source is STILL WRITABLE through the"
    say "           read-only mount. MS_RDONLY applies to the mount it is set on, not to"
    say "           the mounts underneath it. I1 is unprotected for any download path that"
    say "           has its own filesystem below it — a mergerfs branch, a second array"
    say "           disk, an unRAID user share, a per-tracker ZFS dataset."
    say "           -> content is now: $(cat "$ROOT/hostdata/torrents/complete/nested-disk/movie2.mkv")"
    say "           -> the fix is RECURSIVE read-only (mount_setattr(AT_RECURSIVE), kernel"
    say "              >= 5.12), which the container runtime has to ask for explicitly."
else
    say "[NOTE    ] the nested submount was read-only too on this kernel/util-linux."
    say "           Recursive-ro is version-dependent — record the versions with the result."
    say "           kernel=$(uname -r)  util-linux=$(mount --version | head -1)"
fi
say

# --- R5: a NON-recursive bind hides the submount entirely -----------------
say "=== R5: a non-recursive bind mount of a path that has a submount under it ==="
if [ -e "$ROOT/ctrNR/downloads/complete/nested-disk/movie2.mkv" ]; then
    say "[NOTE    ] the nested file is visible through the non-recursive bind"
else
    say "[VERIFIED] a NON-recursive bind mount does not carry submounts: the container sees"
    say "           an EMPTY directory where a whole disk of downloads lives. Every torrent"
    say "           on that disk resolves to PATH_NOT_FOUND, with nothing in the UI to"
    say "           suggest the data exists. Contents through the non-recursive view:"
    say "           [$(ls -A "$ROOT/ctrNR/downloads/complete/nested-disk" 2>/dev/null | tr '\n' ' ')]"
    say "           -> §8.3 step 3 stats the mapped save_path and stops. It should also"
    say "              warn when a mapped directory is EMPTY but the client reports"
    say "              torrents under it — that is this bug, and it is invisible otherwise."
fi
say

# --- R6: a second, writable mount of the same data -------------------------
say "=== R6: a second rw mount of the same data, alongside the :ro one ==="
mkdir -p "$ROOT/ctr/data"
mount --bind "$ROOT/hostdata" "$ROOT/ctr/data"
TARGET="$ROOT/ctr/data/torrents/complete/Some.Release.2009.1080p/movie.mkv"
if echo -n "DESTROYED-VIA-THE-RW-VIEW" > "$TARGET" 2>/dev/null; then
    seen=$(cat "$ROOT/ctr/downloads/complete/Some.Release.2009.1080p/movie.mkv")
    say "[VERIFIED] A second, writable bind mount of the same host directory defeats the"
    say "           read-only mount completely. The file is one inode; :ro is a property of"
    say "           a MOUNT, not of the data."
    say "           -> read back through the :ro view: '$seen'"
    say "           -> DESIGN §11.1's documented compose deliberately uses ONE"
    say "              \`-v /mnt/pool:/data\` mount so that hardlinks work. Adding a :ro"
    say "              downloads mount to that compose gives ZERO protection while looking"
    say "              like it gives some. The two recommendations are incompatible and the"
    say "              docs must say which one the user is choosing."
else
    say "[REFUTED ] the second rw bind mount did not permit the write"; fail=1
fi
say

say "=== CONSEQUENCE ==="
say "    A :ro download mount is a real, cheap, kernel-level backstop for I1 — but a"
say "    backstop, not the mechanism. It is defeated by a second rw mount of the same"
say "    data (R6), it does not reach nested submounts (R4), and it does not stop a"
say "    symlink inside the source tree resolving into a writable one (R3). fsx.FS"
say "    still has to refuse writes to source roots and §6.2 still has to refuse"
say "    symlinks. And it forecloses hardlinking entirely (R2)."

umount -l "$ROOT/ctr/data" "$ROOT/ctr/downloads" "$ROOT/ctr/media" "$ROOT/ctrNR/downloads" 2>/dev/null
umount -l "$ROOT/hostdata/torrents/complete/nested-disk" 2>/dev/null
rm -rf "$ROOT"
exit $fail
INNER
