#!/usr/bin/env bash
# The Linux (WSL) lane of harness-gate (M1-G-GATE-20; README.md, section Lanes).
#
# Run it from a WSL-native clone on ext4, never from /mnt/<drive>:
#
#   git clone /mnt/e/work/github/ai/agent-plugin ~/src/agent-plugin
#   git -C ~/src/agent-plugin checkout <sha>
#   ~/src/agent-plugin/tools/harness-gate/lanes/wsl.sh
#
# It checks the lane first and stops at the first preflight check not met, naming
# it, before any gate runs. It then runs the release rehearsal and the gate with
# --evidence, and leaves the records in
# docs/stories/harness-DSH-M1/evidence/runs/wsl-<sha7>-<UTC yyyymmddThhmmZ>.txt
# (and ...-rehearse.txt). Exit status: the gate's (0 pass, 1 a gate failed, 2
# setup), else the rehearsal's; 3 when a preflight check fails.
#
#   ~/src/agent-plugin/tools/harness-gate/lanes/wsl.sh --guarded-preflight
#
# runs the lane checks and then the guarded preflight alone (implementation-plan
# Stage 6.10, architecture.md I-8, service:linux-sandbox-bwrap): bwrap on an
# absolute path (installed with apt-get when sudo needs no password), then the
# built dsh's `dsh sandbox status` on a scratch home, which must report backend
# bwrap, state Full; anything else fails (exit 3), never skips. It prints the
# report and exits 0 without running a gate. Exit 2 on an unknown argument.
set -euo pipefail

fail() {
	echo "wsl lane: preflight FAILED: $*" >&2
	exit 3
}

guarded_only=
for arg in "$@"; do
	case "$arg" in
	--guarded-preflight) guarded_only=1 ;;
	*)
		echo "wsl lane: unknown argument $arg (usage: wsl.sh [--guarded-preflight])" >&2
		exit 2
		;;
	esac
done

# fstype prints the file-system type of a path; ext4 is reported as "ext2/ext3".
fstype() { stat -f -c %T "$1"; }

# on_ext4 fails, naming what, unless the path is on ext4.
on_ext4() {
	local what=$1 path=$2 type
	type=$(fstype "$path") || fail "$what: cannot stat $path"
	case "$type" in
	ext2/ext3 | ext4) ;;
	9p | v9fs) fail "$what on 9p ($path, $type): use a WSL-native clone on ext4 (README.md, Lanes)" ;;
	drvfs) fail "$what on drvfs ($path): use a WSL-native clone on ext4 (README.md, Lanes)" ;;
	*) fail "$what on $type ($path), not ext4" ;;
	esac
	case "$(cd "$path" && pwd -P)" in
	/mnt/[a-z] | /mnt/[a-z]/*) fail "$what on 9p or drvfs ($path is under /mnt/<drive>): use a WSL-native clone on ext4" ;;
	esac
}

# guarded_preflight is the preflight of a guarded run (service:linux-sandbox-bwrap):
# 7. bwrap resolves to an absolute path; when it is missing it is installed with
#    apt-get if sudo needs no password, else the check fails naming the package;
# 8. the dsh built from this checkout reports, on a scratch home under TMPDIR,
#    backend bwrap, state Full (harness-gate sandbox-preflight); every other report
#    fails, never skips (architecture.md I-8).
guarded_preflight() {
	local bw gdir report verdict
	bw=$(command -v bwrap || true)
	if [ -z "$bw" ] && command -v apt-get >/dev/null && sudo -n true 2>/dev/null; then
		echo "wsl lane: bwrap is not on PATH; installing bubblewrap (sudo apt-get update && sudo apt-get install -y bubblewrap)"
		# A fresh WSL image may have empty or stale package lists.
		sudo -n apt-get update -qq >&2 || fail "apt-get update failed"
		sudo -n apt-get install -y bubblewrap >&2 || fail "apt-get install bubblewrap failed"
		bw=$(command -v bwrap || true)
	fi
	[ -n "$bw" ] || fail "bwrap is not on PATH; install it (sudo apt-get install bubblewrap), README.md, Provisioning the WSL lane"
	case "$bw" in
	/*) ;;
	*) fail "bwrap resolves to $bw, not an absolute path" ;;
	esac
	[ -x "$bw" ] || fail "bwrap at $bw is not executable"

	gdir=$(mktemp -d "$tmp/harness-gate-guarded.XXXXXX")
	trap 'rm -rf "$gdir"' EXIT
	GOWORK=off go -C src/harness build -o "$gdir/dsh" ./cmd/dsh || fail "go build ./cmd/dsh failed"
	report=$("$gdir/dsh" --home "$gdir/home" sandbox status 2>"$gdir/stderr") ||
		fail "dsh sandbox status exited $?: $(cat "$gdir/stderr")"
	echo "wsl lane: dsh sandbox status (bwrap $bw):"
	printf '%s\n' "$report" | sed 's/^/  /'
	verdict=$(printf '%s\n' "$report" | GOWORK=off go -C tools/harness-gate run . sandbox-preflight) ||
		fail "guarded preflight: ${verdict:-harness-gate sandbox-preflight did not run}"
	rm -rf "$gdir"
	trap - EXIT
	echo "wsl lane: guarded preflight passed: bwrap $bw; ${verdict#harness-gate sandbox-preflight: passed: }"
}

# The checkout is the one holding this script, else the working directory (a script
# fed on stdin).
here=$(cd "$(dirname "${BASH_SOURCE[0]:-.}")" && pwd -P)
root=$(git -C "$here" rev-parse --show-toplevel 2>/dev/null || git rev-parse --show-toplevel 2>/dev/null) ||
	fail "not inside a git checkout: $here or $(pwd)"
cd "$root"

# A non-login shell has not read ~/.profile, which puts /usr/local/go/bin on PATH.
case ":$PATH:" in
*:/usr/local/go/bin:*) ;;
*) [ -x /usr/local/go/bin/go ] && PATH="/usr/local/go/bin:$PATH" ;;
esac

# 1. The checkout is on ext4, never 9p or drvfs.
on_ext4 "checkout" .

# 2. Not root: root bypasses the permission tests of the storage packages.
[ "$(id -u)" != 0 ] || fail "running as root (id -u is 0); use the non-root default user"

# 3. go is the floor of src/harness/go.mod, installed locally (no download).
floor=$(awk '$1 == "go" { print $2; exit }' src/harness/go.mod)
[ -n "$floor" ] || fail "no go line in src/harness/go.mod"
gobin=$(command -v go) || fail "go is not on PATH (install go$floor into /usr/local/go)"
case "$gobin" in
/mnt/*) fail "go resolves to the Windows toolchain $gobin; set appendWindowsPath=false in /etc/wsl.conf" ;;
esac
got=$(GOTOOLCHAIN=local go version | awk '{ print $3 }')
[ "$got" = "go$floor" ] || fail "go version is $got, not the floor go$floor (src/harness/go.mod)"
export GOTOOLCHAIN="go$floor"

# 4. go env CC resolves to a C compiler (cgo, and so -race).
cc=$(CGO_ENABLED=1 go env CC)
cc=${cc%% *}
[ -n "$cc" ] || fail "go env CC is empty; install build-essential"
command -v "$cc" >/dev/null || fail "go env CC ($cc) does not resolve; install build-essential"

# 5. The tree is clean, with core.autocrlf false or unset.
autocrlf=$(git config --get core.autocrlf || true)
case "$autocrlf" in
"" | false) ;;
*) fail "core.autocrlf is $autocrlf; unset it (.gitattributes forces eol=lf)" ;;
esac
dirty=$(git -c core.autocrlf=false status --porcelain --untracked-files=all)
[ -z "$dirty" ] || fail "the tree is not clean:
$dirty"

# 6. TMPDIR is on ext4.
tmp=${TMPDIR:-/tmp}
on_ext4 "TMPDIR" "$tmp"

sha=$(git rev-parse HEAD)
stamp=$(date -u +%Y%m%dT%H%MZ)
runs="$root/docs/stories/harness-DSH-M1/evidence/runs"
record="$runs/wsl-${sha:0:7}-$stamp.txt"
rehearsal="$runs/wsl-${sha:0:7}-$stamp-rehearse.txt"
echo "wsl lane: preflight passed: ext4 checkout, uid $(id -u), $got, CC=$cc, clean tree, TMPDIR=$tmp"

if [ -n "$guarded_only" ]; then
	guarded_preflight
	exit 0
fi

# The rehearsal runs first, on the clean tree, so its vcs.modified is the commit's;
# its output waits outside the tree until the gate has recorded the run.
scratch=$(mktemp "$tmp/harness-gate-rehearse.XXXXXX")
trap 'rm -f "$scratch"' EXIT
set +e
{
	echo "harness-gate rehearse record"
	echo "commit: $sha"
	echo "started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	GOWORK=off CI=1 go -C tools/harness-gate run . rehearse --root ../..
	echo "exit: $?"
	echo "ended: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
} 2>&1 | tee "$scratch"
rehearse=$(awk '/^exit: / { code = $2 } END { print code + 0 }' "$scratch")

mkdir -p "$runs"
GOWORK=off go -C tools/harness-gate run . --root ../.. --evidence "$record"
gate=$?
set -e
cp "$scratch" "$rehearsal"
echo "sha256: $(sha256sum "$rehearsal" | awk '{ print $1 }')" >>"$rehearsal"

echo "wsl lane: gate exit $gate, record $record"
echo "wsl lane: rehearse exit $rehearse, record $rehearsal"
[ "$gate" -ne 0 ] && exit "$gate"
exit "$rehearse"
