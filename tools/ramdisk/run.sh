#!/bin/sh
# Run a command with every throwaway write of the test / conformance harness on
# a RAM-backed volume, then tear the volume down.
#
#   tools/ramdisk/run.sh [-s SIZE_MB] -- go test ./...
#   tools/ramdisk/run.sh -s 16384 -- go run ./tools/conformance -compat=both
#
# The point is SSD wear, not speed: a full suite writes several GB of IR,
# objects and binaries that are deleted seconds later. KML_SCRATCH (see
# internal/scratch) moves the harness's temp root and the conformance workdir;
# GOTMPDIR moves the go tool's own link output (the ~40 MB test binary).
#
# macOS: hdiutil RAM device + APFS volume (built in, no root).
# Linux: a tmpfs mount when root, else a directory under /dev/shm (already
#        tmpfs, exec-mounted on the common distros). In Docker prefer
#        `--tmpfs /scratch:exec,size=8g -e KML_SCRATCH=/scratch` — note `exec`:
#        the tests run the binaries they build from there.
# Windows: see run.ps1.
set -eu

size_mb=8192
while [ $# -gt 0 ]; do
	case "$1" in
	-s) size_mb=$2; shift 2 ;;
	--) shift; break ;;
	*) break ;;
	esac
done
[ $# -gt 0 ] || { echo "usage: $0 [-s SIZE_MB] -- command..." >&2; exit 2; }

dev="" ; mnt="" ; owned_mount=0
cleanup() {
	status=$?
	trap - EXIT INT TERM
	case "$(uname -s)" in
	Darwin) [ -n "$dev" ] && hdiutil detach "$dev" -force >/dev/null 2>&1 ;;
	*)
		if [ "$owned_mount" = 1 ]; then umount "$mnt" 2>/dev/null; rmdir "$mnt" 2>/dev/null
		elif [ -n "$mnt" ]; then rm -rf "$mnt"; fi ;;
	esac
	exit $status
}
trap cleanup EXIT INT TERM

case "$(uname -s)" in
Darwin)
	# ram:// takes a count of 512-byte sectors.
	dev=$(hdiutil attach -nomount "ram://$((size_mb * 2048))" | awk '{print $1}')
	diskutil erasevolume APFS KMLScratch "$dev" >/dev/null
	mnt=/Volumes/KMLScratch
	;;
*)
	if [ "$(id -u)" = 0 ]; then
		mnt=$(mktemp -d /tmp/kml-scratch.XXXXXX)
		mount -t tmpfs -o "size=${size_mb}m,exec" tmpfs "$mnt"
		owned_mount=1
	else
		[ -d /dev/shm ] || { echo "no /dev/shm and not root: cannot create a RAM volume" >&2; exit 1; }
		mnt=$(mktemp -d /dev/shm/kml-scratch.XXXXXX)
	fi
	;;
esac

export KML_SCRATCH="$mnt"
export GOTMPDIR="$mnt"
echo "ramdisk: $mnt (${size_mb} MB)" >&2
"$@"
