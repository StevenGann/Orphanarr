#!/usr/bin/env bash
# Orphanarr — Docker bind-mount hardlink verification.
#
# WHAT THIS PROVES
# ----------------
# The single most dangerous half-truth in the *arr ecosystem is "hardlinks work
# if the files are on the same filesystem." They are not. The kernel's test is
# stricter.
#
#   fs/namei.c, filename_linkat():
#       error = -EXDEV;
#       if (old_path.mnt != new_path.mnt)
#               goto out_dput;
#
# It compares *vfsmount pointers*, not superblocks. Two Docker bind mounts of
# two directories on ONE host filesystem are ONE filesystem (identical st_dev)
# but TWO vfsmounts — so link(2) returns EXDEV anyway.
#
# Consequence for Orphanarr: `os.stat(a).st_dev == os.stat(b).st_dev` is a
# NECESSARY but NOT SUFFICIENT precondition for hardlinking. The only reliable
# probe is to attempt a link and catch EXDEV. Any design that gates on st_dev
# alone will confidently choose "hardlink" and then fail at runtime for every
# user who followed the common two-volume Docker recipe.
#
# This script reproduces both layouts without Docker and without root, using an
# unprivileged user+mount namespace (`unshare -Urm`), which yields the same
# vfsmount topology a container has.
#
# Run:  bash tests/verification/bindmount_hardlink_test.sh
# Exit: 0 if reality matches the claims above.

set -u

if ! unshare -Urm --propagation private true 2>/dev/null; then
    echo "SKIP: unprivileged user+mount namespaces unavailable on this host" >&2
    exit 2
fi

unshare -Urm --propagation private bash -s <<'INNER'
set -u
fail=0
say() { printf '%s\n' "$*"; }

ROOT=$(mktemp -d)
# One host filesystem, the layout nearly every *arr tutorial produces.
mkdir -p "$ROOT/hostdata/torrents/complete/Some.Release.2009.1080p.BluRay.x264-GRP"
mkdir -p "$ROOT/hostdata/media/Movies"
echo payload > "$ROOT/hostdata/torrents/complete/Some.Release.2009.1080p.BluRay.x264-GRP/movie.mkv"

SRC="$ROOT/hostdata/torrents/complete/Some.Release.2009.1080p.BluRay.x264-GRP/movie.mkv"

say "=== BASELINE: no bind mounts, one filesystem ==="
if ln "$SRC" "$ROOT/hostdata/media/Movies/baseline.mkv" 2>/dev/null; then
    say "[VERIFIED] link() succeeds within a single mount"
else
    say "[REFUTED ] link() failed inside a single mount"; fail=1
fi

say
say "=== LAYOUT A: the common Docker mistake — two separate bind mounts ==="
say "    docker run -v /hostdata/torrents:/downloads -v /hostdata/media:/media"
mkdir -p "$ROOT/ctrA/downloads" "$ROOT/ctrA/media"
mount --bind "$ROOT/hostdata/torrents" "$ROOT/ctrA/downloads"
mount --bind "$ROOT/hostdata/media"    "$ROOT/ctrA/media"

DEV_DL=$(stat -c %d "$ROOT/ctrA/downloads")
DEV_MD=$(stat -c %d "$ROOT/ctrA/media")
say "    st_dev(downloads)=$DEV_DL  st_dev(media)=$DEV_MD"
if [ "$DEV_DL" = "$DEV_MD" ]; then
    say "[VERIFIED] st_dev is IDENTICAL across the two bind mounts (same filesystem)"
else
    say "[REFUTED ] st_dev differed; this host's bind mounts do not share a superblock"; fail=1
fi

A_SRC="$ROOT/ctrA/downloads/complete/Some.Release.2009.1080p.BluRay.x264-GRP/movie.mkv"
err=$(ln "$A_SRC" "$ROOT/ctrA/media/Movies/linked.mkv" 2>&1)
rc=$?
if [ $rc -ne 0 ] && printf '%s' "$err" | grep -qi 'cross-device\|Invalid cross'; then
    say "[VERIFIED] link() across the two bind mounts FAILS with EXDEV despite equal st_dev"
    say "           -> ln said: ${err}"
    say "           -> an st_dev-only precondition check would have said 'hardlink is safe'. It is not."
else
    say "[REFUTED ] expected EXDEV, got rc=$rc err='${err}'"; fail=1
fi

say
say "=== LAYOUT B: the TRaSH-recommended single mount ==="
say "    docker run -v /hostdata:/data     (then /data/torrents and /data/media)"
mkdir -p "$ROOT/ctrB/data"
mount --bind "$ROOT/hostdata" "$ROOT/ctrB/data"
B_SRC="$ROOT/ctrB/data/torrents/complete/Some.Release.2009.1080p.BluRay.x264-GRP/movie.mkv"
if ln "$B_SRC" "$ROOT/ctrB/data/media/Movies/linked-ok.mkv" 2>/dev/null; then
    ln_count=$(stat -c %h "$B_SRC")
    say "[VERIFIED] link() SUCCEEDS under one shared bind mount; st_nlink=$ln_count"
    say "           -> the fix is a mount-topology fix, not a filesystem fix"
else
    say "[REFUTED ] link() failed under a single shared bind mount"; fail=1
fi

say
say "=== CONSEQUENCE ==="
say "    The only sound runtime probe is: attempt link(2) on a real file and"
say "    catch EXDEV. st_dev comparison alone produces false positives exactly"
say "    in the configuration most users have."

umount "$ROOT/ctrA/downloads" "$ROOT/ctrA/media" "$ROOT/ctrB/data" 2>/dev/null
rm -rf "$ROOT"
exit $fail
INNER
