#!/usr/bin/env bash
#
# Self-test for install.sh: run the real installer against archives built from
# the current commit, served over loopback HTTP, and try to falsify it.
#
# Why the test is shaped this way: the installer is the part of this project that
# runs on machines nobody here can see, with `curl | bash` as the documented way
# in, and it is the part users are least likely to read. Its own log is therefore
# not evidence of anything -- every check below fails if the log is a lie or if
# the step it claims to have done was skipped.
#
#   * A tampered checksums.txt must be refused: non-zero exit, with the mismatch
#     named, and nothing installed. This is the single most important check in the
#     file. An installer that silently skips verification is worse than no
#     installer at all, because it advertises a guarantee it does not provide, and
#     this is the only case that proves the guarantee is real.
#   * An asset missing from checksums.txt must be refused rather than installed
#     anyway: half a checksums file must not become a pass.
#   * A missing asset must fail the install, not fall back to another architecture.
#   * A platform this release does not build must be refused by name. `uname` is
#     stubbed to ask for them, because that is the only way to reach the branch on
#     a machine whose own architecture is one of the supported ones.
#   * The installed binary's bytes must equal the bytes in the archive just built,
#     so "installed successfully" cannot mean "installed something else".
#   * Re-running must leave exactly one sael. An installer that appends a new
#     symlink or version directory on every run turns an upgrade into a growing
#     mess that only shows up months later.
#   * Every documented way of saying where to install has to mean the same place.
#     `--install-dir` once put its symlink in the user's real ~/.local/bin while
#     `SAEL_INSTALL_DIR` put it alongside the install root, so the flag form
#     quietly moved an existing sael instead of isolating an install; the case
#     that catches it asserts on the absence under HOME, not only on the presence
#     under the install root, because a fix that writes to both would pass the
#     weaker check.
#
# Everything is written under one temp root that is removed on exit, so it never
# touches the real ~/.local/bin, ~/.local/share/sael or ~/.Sael. That is not
# politeness: a self-test that installs into the developer's real PATH would
# overwrite the sael they are using to investigate the failure.
#
# Windows has the equivalent in scripts/install-selftest.ps1. There is no bash
# there, and a Windows test that quietly does not run is the failure mode the CI
# workflow exists to prevent, so the two are kept in step by hand.
#
# Usage: scripts/install-selftest.sh [--dist DIR] [--installer FILE] [--version VERSION]
#
# Requires: bash, curl, tar, go (to build scripts/servedist), and a dist/ built by
# `make release-snapshot`.

set -euo pipefail

DIST="dist"
INSTALLER="install.sh"
VERSION=""

usage() {
    # The header comment is the documentation, so it is printed rather than
    # repeated: a second copy of the usage text is a copy that goes stale. Stops
    # at the first line that is not a comment, which is where the code starts.
    awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}

while [ $# -gt 0 ]; do
    case "$1" in
        --dist)      [ $# -ge 2 ] || { echo "--dist needs a value" >&2; exit 2; }; DIST="$2"; shift 2 ;;
        --installer) [ $# -ge 2 ] || { echo "--installer needs a value" >&2; exit 2; }; INSTALLER="$2"; shift 2 ;;
        --version)   [ $# -ge 2 ] || { echo "--version needs a value" >&2; exit 2; }; VERSION="$2"; shift 2 ;;
        -h|--help)   usage; exit 0 ;;
        *)           echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

GO="${GO:-go}"

# The exit status of the last check is what CI reads, so failures are counted
# rather than thrown: a run that stops at the first failure hides the other four,
# and the report is more useful when it is a list than when it is a queue.
FAILURES=0
pass() { printf '   ok   %s\n' "$*"; }
fail() { printf '   FAIL %s\n' "$*"; FAILURES=$((FAILURES + 1)); }

# Print a command the way the reader would type it, so the log can be replayed by
# hand without reading this file.
show() { printf '$ %s\n' "$*"; }

sha256_file() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        echo "no SHA-256 tool (shasum or sha256sum) on PATH" >&2
        exit 2
    fi
}

# Invoke the real installer with the testing overrides. PATH is set on the
# command rather than exported: the stub directory is how the unsupported-platform
# cases lie about uname, and it must not leak into the next case.
#
# HOME is replaced as well, and that is not tidiness. install.sh falls back to
# `$HOME/.local/bin` for the symlink whenever it does not think it was told
# otherwise -- which is exactly the behaviour the --install-dir case below is
# about. A test that left the real HOME in place would write into the developer's
# own ~/.local/bin and repoint the sael they are using to investigate. Every case
# passes its own HOME under the temp root, so "the real home was not touched" is
# true by construction rather than by hope.
#
# An EMPTY install_dir means SAEL_INSTALL_DIR is deliberately left unset, so the
# --install-dir flag is the only source of the install root. Setting both would
# make the flag and the environment agree by construction, and that is how the
# original bug hid: with SAEL_INSTALL_DIR also set, the flag form looked correct.
#
#   invoke_install <install_dir_or_empty> <base_url> <stub_dir_or_empty> <home_dir> [installer args...]
invoke_install() {
    local install_dir="$1" base_url="$2" stub="$3" home_dir="$4"
    shift 4
    local run_path="$PATH"
    if [ -n "$stub" ]; then run_path="$stub:$PATH"; fi
    if [ -z "$install_dir" ]; then
        PATH="$run_path" \
        HOME="$home_dir" \
        SAEL_RELEASE_BASE_URL="$base_url" \
        SAEL_HOME="$home_dir" \
            "$INSTALLER" --skip-setup "$@"
    else
        PATH="$run_path" \
        HOME="$home_dir" \
        SAEL_RELEASE_BASE_URL="$base_url" \
        SAEL_INSTALL_DIR="$install_dir" \
        SAEL_HOME="$home_dir" \
            "$INSTALLER" --skip-setup "$@"
    fi
}

# Run the installer and capture its output and status without letting a non-zero
# exit kill this script: several cases expect one. $out and $rc are set for the
# caller.
run_install() {
    out=""
    rc=0
    if out="$(invoke_install "$@" 2>&1)"; then rc=0; else rc=$?; fi
}

# Counting and listing through find rather than `ls`: the install directory is
# named after a version string, and `ls` mangles anything that is not a plain
# name. A test that trips over its own tooling reports failures that are not the
# installer's.
count_entries() { find "$1" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' '; }
list_entries() { find "$1" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort | tr '\n' ' '; }

assert_nothing_installed() {
    local dir="$1" found
    found="$(find "$dir" -name 'sael' -print -quit 2>/dev/null || true)"
    if [ -n "$found" ]; then
        fail "an unverified install left a binary behind: $found"
    else
        pass "nothing installed under $dir"
    fi
}

tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/sael-selftest.XXXXXX")"
SERVER_PID=""
cleanup() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$tmp_root"
}
trap cleanup EXIT INT TERM

[ -d "$DIST" ] || { echo "no $DIST directory; run 'make release-snapshot' first" >&2; exit 2; }
[ -f "$INSTALLER" ] || { echo "no installer at $INSTALLER" >&2; exit 2; }
INSTALLER="$(cd "$(dirname "$INSTALLER")" && pwd)/$(basename "$INSTALLER")"
DIST="$(cd "$DIST" && pwd)"

# The platform triple is derived here exactly as install.sh derives it, because
# the point is to install the archive this machine would really be given.
case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux)  OS=linux ;;
    *) echo "this self-test drives install.sh, which runs on Linux and macOS only" >&2; exit 2 ;;
esac
case "$(uname -m)" in
    arm64|aarch64) ARCH=arm64 ;;
    x86_64|amd64)  ARCH=amd64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac

# The version comes out of the archive name rather than from an argument. A
# version passed in by hand is a second copy of a fact the build already recorded,
# and the copy that drifts is always the one the test uses.
if [ -z "$VERSION" ]; then
    shopt -s nullglob
    matches=("$DIST"/sael_*_"${OS}"_"${ARCH}".tar.gz)
    shopt -u nullglob
    if [ ${#matches[@]} -eq 0 ]; then
        echo "no sael_*_${OS}_${ARCH}.tar.gz in $DIST; run 'make release-snapshot'" >&2
        exit 2
    fi
    if [ ${#matches[@]} -gt 1 ]; then
        echo "more than one candidate archive in $DIST; using $(basename "${matches[0]}")" >&2
    fi
    base="$(basename "${matches[0]}")"
    VERSION="${base#sael_}"
    VERSION="${VERSION%_"${OS}"_"${ARCH}".tar.gz}"
fi
ASSET="sael_${VERSION}_${OS}_${ARCH}.tar.gz"
ARCHIVE="$DIST/$ASSET"
[ -f "$ARCHIVE" ] || { echo "no $ARCHIVE in $DIST" >&2; exit 2; }

printf '\ninstall.sh self-test\n'
printf '  installer: %s\n' "$INSTALLER"
printf '  dist:      %s\n' "$DIST"
printf '  asset:     %s\n' "$ASSET"
printf '  platform:  %s/%s\n\n' "$OS" "$ARCH"

# --- the mirror ---------------------------------------------------------------
#
# Four copies of dist/, each served from its own path, so one long-lived server
# answers every case: ok/ is the honest release, tampered/ has one hex digit of
# the archive's checksum flipped, missing/ omits the archive's line entirely, and
# absent/ does not exist at all.
MIRROR="$tmp_root/mirror"
mkdir -p "$MIRROR"
cp -R "$DIST" "$MIRROR/ok"
cp -R "$MIRROR/ok" "$MIRROR/tampered"
cp -R "$MIRROR/ok" "$MIRROR/missing"

# Flip the first hex digit of the hash for our asset: "0" becomes "1" and
# anything else becomes "0", so the replacement is always different from what was
# there. OFS is the two spaces goreleaser writes, so the file stays a valid
# checksums file that fails for exactly one reason.
tamper() {
    local src="$1" dst="$2"
    awk -v want="$ASSET" '
        BEGIN { OFS = "  " }
        {
            name = $2; sub(/^\*/, "", name)
            if (!done && name == want) {
                sub(/^[0-9a-fA-F]/, (substr($1, 1, 1) == "0" ? "1" : "0"), $1)
                done = 1
            }
            print
        }
    ' "$src" >"$dst"
}

# Drop the archive's line, which is the "this release does not vouch for this
# asset" case.
drop_entry() {
    local src="$1" dst="$2"
    awk -v want="$ASSET" '
        { name = $2; sub(/^\*/, "", name); if (name == want) next; print }
    ' "$src" >"$dst"
}

orig_hash="$(awk -v want="$ASSET" '{ n = $2; sub(/^\*/, "", n); if (n == want) { print $1; exit } }' "$MIRROR/ok/checksums.txt")"
tamper "$MIRROR/ok/checksums.txt" "$MIRROR/tampered/checksums.txt"
drop_entry "$MIRROR/ok/checksums.txt" "$MIRROR/missing/checksums.txt"
tampered_hash="$(awk -v want="$ASSET" '{ n = $2; sub(/^\*/, "", n); if (n == want) { print $1; exit } }' "$MIRROR/tampered/checksums.txt")"

# Test the test. If the mutation quietly did nothing -- the asset is renamed, the
# checksums format changed, the awk lost a line -- then the negative cases below
# would be asserting against a file that is not actually corrupt, and a passing
# run would mean nothing at all.
printf 'mirror sanity\n'
if [ -z "$orig_hash" ]; then
    fail "$ASSET is not listed in dist/checksums.txt, so the mirror cannot be built"
elif [ "${#orig_hash}" != "64" ]; then
    fail "the checksum for $ASSET is ${#orig_hash} characters, not 64"
elif [ "$tampered_hash" = "$orig_hash" ]; then
    fail "tampering did not change the checksum for $ASSET"
else
    pass "tampered checksum is $orig_hash -> $tampered_hash"
fi
if [ -n "$(awk -v want="$ASSET" '{ n = $2; sub(/^\*/, "", n); if (n == want) { print; exit } }' "$MIRROR/missing/checksums.txt")" ]; then
    fail "$ASSET is still listed in the mirror's missing/ checksums.txt"
else
    pass "$ASSET is absent from the mirror's missing/ checksums.txt"
fi
[ -s "$MIRROR/ok/$ASSET" ] || fail "$ASSET is missing from the mirror"
printf '\n'

# --- serve the mirror ---------------------------------------------------------
#
# The server is built rather than run through `go run`, so the process this script
# kills is the one holding the port. `go run` leaves the child behind after the
# parent is killed, and a stray listener is a failure that looks like a port
# conflict in whatever runs next.
show "$GO build -o $tmp_root/servedir ./scripts/servedist/main.go"
"$GO" build -o "$tmp_root/servedir" ./scripts/servedist/main.go

SERVER_LOG="$tmp_root/server.log"
show "$tmp_root/servedir $MIRROR 0 &"
"$tmp_root/servedir" "$MIRROR" 0 >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

# Port 0 lets the kernel pick, and the port is read back from the server's own
# readiness line rather than from a constant here: a fixed port turns a leftover
# server, or two CI jobs on one runner, into a failure that has nothing to do
# with the installer.
BASE=""
for _ in $(seq 1 100); do
    BASE="$(awk '/^listening on /{ print $3; exit }' "$SERVER_LOG")"
    if [ -n "$BASE" ]; then break; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "servedir exited before listening:" >&2
        cat "$SERVER_LOG" >&2
        exit 2
    fi
    sleep 0.1
done
if [ -z "$BASE" ]; then
    echo "servedir never reported a listening address:" >&2
    cat "$SERVER_LOG" >&2
    exit 2
fi
printf 'serving %s at %s\n\n' "$MIRROR" "$BASE"

# --- 1. the happy path --------------------------------------------------------
printf '=== happy path\n'
happy_dir="$tmp_root/install-happy"
show "SAEL_RELEASE_BASE_URL=$BASE/ok SAEL_INSTALL_DIR=$happy_dir $INSTALLER --version $VERSION --skip-setup"
run_install "$happy_dir" "$BASE/ok" "" "$tmp_root/home-happy" --version "$VERSION"
if [ "$rc" -eq 0 ]; then
    pass "installer exited 0"
else
    fail "installer exited $rc"
    printf '%s\n' "$out"
fi
# The log is checked for the verification line because the next install could
# otherwise pass while having downloaded unverified bytes: a run that verified and
# a run that skipped are identical except for that line.
if printf '%s' "$out" | grep -q 'SHA-256 verified'; then
    pass "the log shows the archive was verified"
else
    fail "the log does not show SHA-256 verification"
fi

exe="$happy_dir/bin/sael"
if [ -x "$exe" ]; then
    pass "installed $exe"
else
    fail "no executable at $exe"
fi
run_exe() { out=""; rc=0; if out="$("$exe" "$@" 2>&1)"; then rc=0; else rc=$?; fi; }
run_exe --version
if [ "$rc" -eq 0 ] && [ "$out" = "sael $VERSION" ]; then
    pass "sael --version prints '$out'"
else
    fail "sael --version printed '$out' (status $rc), wanted 'sael $VERSION'"
fi

# The version string is injected by goreleaser; a build whose ldflags drifted
# reports "devel" and would still exit 0, so it is named as a failure rather than
# left to the equality check above to imply.
if printf '%s' "$out" | grep -q 'devel'; then
    fail "the installed binary reports a version of devel: the ldflags injection did not reach it"
fi

# Stronger than the version string: the bytes on disk are the bytes in the
# archive that was just built. A stale or mismatched binary that happens to
# report the same version passes the check above and fails this one.
mkdir -p "$tmp_root/extract-check"
if tar -xzf "$ARCHIVE" -C "$tmp_root/extract-check"; then
    if [ "$(sha256_file "$tmp_root/extract-check/sael")" = "$(sha256_file "$exe")" ]; then
        pass "the installed binary is byte for byte the one in $ASSET"
    else
        fail "the installed binary differs from the sael inside $ASSET"
    fi
else
    fail "could not extract $ASSET to compare against the installed binary"
fi
printf '\n'

# --- 2. idempotency -----------------------------------------------------------
printf '=== re-running the installer\n'
show "SAEL_RELEASE_BASE_URL=$BASE/ok SAEL_INSTALL_DIR=$happy_dir $INSTALLER --version $VERSION --skip-setup   (second run)"
run_install "$happy_dir" "$BASE/ok" "" "$tmp_root/home-happy" --version "$VERSION"
if [ "$rc" -eq 0 ]; then
    pass "the second run exited 0"
else
    fail "the second run exited $rc"
    printf '%s\n' "$out"
fi
bin_entries="$(count_entries "$happy_dir/bin")"
dir_entries="$(count_entries "$happy_dir")"
if [ "$bin_entries" = "1" ] && [ -L "$happy_dir/bin/sael" ]; then
    # bin/ holds the symlink and nothing else. The staged name it is renamed from
    # is gone by the end of a run, and a leftover one would be exactly the sort of
    # thing a second run leaves behind.
    pass "exactly one entry in bin/: $(list_entries "$happy_dir/bin")"
else
    fail "bin/ holds $bin_entries entries: $(list_entries "$happy_dir/bin")"
fi
if [ "$dir_entries" = "2" ]; then
    # bin/ and the one version directory: an upgrade that appends rather than
    # replaces shows up here as a third.
    pass "exactly one version directory: $(list_entries "$happy_dir")"
else
    fail "$dir_entries entries under $happy_dir: $(list_entries "$happy_dir")"
fi
run_exe --version
if [ "$out" = "sael $VERSION" ]; then
    pass "sael --version still prints '$out'"
else
    fail "sael --version printed '$out' after the second run"
fi
printf '\n'

# --- 2b. the tag as it appears on the releases page ---------------------------
#
# The published tag is `cli/v0.0.1-rc1`, not `v0.0.1-rc1`: a subdirectory module's
# tag has to carry the directory name. A user who pastes that tag must not get a
# 404 for an asset name they cannot see is wrong, so the prefix is stripped and the
# version directory is the bare number either way.
printf '=== the tag as published, with its module prefix\n'
tag_dir="$tmp_root/install-tagged"
show "SAEL_RELEASE_BASE_URL=$BASE/ok SAEL_INSTALL_DIR=$tag_dir $INSTALLER --version cli/v$VERSION --skip-setup"
run_install "$tag_dir" "$BASE/ok" "" "$tmp_root/home-tagged" --version "cli/v$VERSION"
if [ "$rc" -eq 0 ]; then
    pass "the installer accepted 'cli/v$VERSION'"
else
    fail "the installer exited $rc for --version cli/v$VERSION"
    printf '%s\n' "$out"
fi
if [ "$(count_entries "$tag_dir")" = "2" ] && [ -d "$tag_dir/$VERSION" ]; then
    pass "the version directory is the bare number: $VERSION"
else
    fail "unexpected install root: $(list_entries "$tag_dir")"
fi
printf '\n'

# --- 2c. --install-dir takes the symlink with it ------------------------------
#
# The regression case. `--install-dir` and `SAEL_INSTALL_DIR` mean the same thing
# and must therefore put the binary and the symlink in the same place; the flag
# form used to leave the symlink at the default `$HOME/.local/bin`, which is the
# one directory a relocated install must not touch. On a machine that already had
# sael, `install.sh --install-dir /tmp/x` silently repointed the user's real
# `~/.local/bin/sael` at the throwaway install, and the isolation the flag is
# chosen for was not isolation at all.
#
# The env-form case above is the control, and the two differ in nothing but flag
# versus environment variable -- same temp HOME, same temp install root -- so a
# failure here is attributable to the flag handling rather than to the case.
#
# The assertion on `$HOME/.local/bin` is the important half. Asserting only that
# `<install-dir>/bin/sael` exists would pass on a "fix" that wrote the symlink to
# both places, which is still the bug for the person whose real sael moved.
printf '=== --install-dir takes the symlink with it\n'
flag_dir="$tmp_root/install-flag"
flag_home="$tmp_root/home-flag"
# SAEL_INSTALL_DIR is left unset on purpose: the flag has to be the only thing
# telling the installer where to go, or the case proves nothing.
show "HOME=$flag_home SAEL_RELEASE_BASE_URL=$BASE/ok $INSTALLER --version $VERSION --install-dir $flag_dir --skip-setup   (SAEL_INSTALL_DIR unset)"
run_install "" "$BASE/ok" "" "$flag_home" --install-dir "$flag_dir" --version "$VERSION"
if [ "$rc" -eq 0 ]; then
    pass "installer exited 0"
else
    fail "installer exited $rc"
    printf '%s\n' "$out"
fi
if [ -L "$flag_dir/bin/sael" ]; then
    pass "the symlink is in the install root: $flag_dir/bin/sael"
else
    fail "no symlink at $flag_dir/bin/sael (found: $(list_entries "$flag_dir"))"
fi
if [ -e "$flag_home/.local/bin/sael" ]; then
    fail "$flag_home/.local/bin/sael was created; a relocated install must not write into HOME"
else
    pass "nothing was written to \$HOME/.local/bin"
fi
# The directory itself, not just the symlink: the installer creates the symlink
# directory before linking, so "the directory exists but is empty" is still a
# modification of the user's PATH directory.
if [ -d "$flag_home/.local/bin" ]; then
    fail "$flag_home/.local/bin was created"
else
    pass "\$HOME/.local/bin was not created"
fi
if [ -x "$flag_dir/bin/sael" ]; then
    run_exe --version
    if [ "$out" = "sael $VERSION" ]; then
        pass "sael --version through the relocated symlink prints '$out'"
    else
        fail "sael --version through the relocated symlink printed '$out'"
    fi
else
    fail "cannot run $flag_dir/bin/sael: it is not there"
fi
printf '\n'

# --- 2d. --bin-dir still overrides both ---------------------------------------
#
# --bin-dir names the symlink directory outright, so it wins over the default the
# install root would otherwise imply and over $HOME/.local/bin. An install root
# that is asked for a symlink somewhere else must not also leave one in its own
# bin/ -- that would be two sael reachable from two paths, which is the upgrade
# mess the single symlink exists to avoid.
#
# SAEL_INSTALL_DIR is unset here as well, so this is the flag-only form: the two
# flags alone have to agree with each other and with nothing else.
printf '=== --bin-dir overrides both defaults\n'
bindir_root="$tmp_root/install-bindir"
bindir_bin="$tmp_root/custom-bin"
bindir_home="$tmp_root/home-bindir"
show "HOME=$bindir_home SAEL_RELEASE_BASE_URL=$BASE/ok $INSTALLER --version $VERSION --install-dir $bindir_root --bin-dir $bindir_bin --skip-setup   (SAEL_INSTALL_DIR unset)"
run_install "" "$BASE/ok" "" "$bindir_home" --install-dir "$bindir_root" --bin-dir "$bindir_bin" --version "$VERSION"
if [ "$rc" -eq 0 ]; then
    pass "installer exited 0"
else
    fail "installer exited $rc"
    printf '%s\n' "$out"
fi
if [ -L "$bindir_bin/sael" ]; then
    pass "the symlink is where --bin-dir said: $bindir_bin/sael"
else
    fail "no symlink at $bindir_bin/sael"
fi
if [ -e "$bindir_root/bin/sael" ]; then
    fail "--bin-dir did not override the install root's own bin/: $bindir_root/bin/sael exists"
else
    pass "no second symlink under the install root"
fi
if [ -e "$bindir_home/.local/bin/sael" ]; then
    fail "--bin-dir did not override \$HOME/.local/bin"
else
    pass "nothing was written to \$HOME/.local/bin"
fi
printf '\n'

# --- 3. tampered checksums.txt is refused -------------------------------------
printf '=== tampered checksums.txt (the one that matters)\n'
show "SAEL_RELEASE_BASE_URL=$BASE/tampered SAEL_INSTALL_DIR=$tmp_root/install-tampered $INSTALLER --version $VERSION --skip-setup"
tampered_dir="$tmp_root/install-tampered"
run_install "$tampered_dir" "$BASE/tampered" "" "$tmp_root/home-tampered" --version "$VERSION"
if [ "$rc" -ne 0 ]; then
    pass "the installer refused a tampered archive (exit $rc)"
else
    fail "the installer exited 0 on a tampered checksums.txt"
fi
if printf '%s' "$out" | grep -q 'Checksum mismatch'; then
    pass "the refusal names the reason: $(printf '%s' "$out" | grep 'Checksum mismatch' | head -1)"
else
    fail "the installer failed without reporting a checksum mismatch"
    printf '%s\n' "$out"
fi
assert_nothing_installed "$tampered_dir"
printf '\n'

# --- 3b. the control for 3 ----------------------------------------------------
#
# Without this, case 3 proves only that *something* went wrong, and a broken tar
# or a truncated download would pass it. Installing the same mirror with
# --no-verify must succeed: same archives, same manifest, only the comparison
# skipped. Together the two cases isolate the rejection to the checksum step
# rather than to anything else about the mirror.
printf '=== the control: the same mirror with --no-verify\n'
control_dir="$tmp_root/install-control"
show "SAEL_RELEASE_BASE_URL=$BASE/tampered SAEL_INSTALL_DIR=$control_dir $INSTALLER --version $VERSION --skip-setup --no-verify"
run_install "$control_dir" "$BASE/tampered" "" "$tmp_root/home-control" --version "$VERSION" --no-verify
if [ "$rc" -eq 0 ]; then
    pass "the archive installs when verification is switched off (exit 0)"
else
    fail "the mirror does not install even with --no-verify (exit $rc), so case 3 is not about the checksum"
    printf '%s\n' "$out"
fi
if printf '%s' "$out" | grep -q -- '--no-verify'; then
    pass "the skipped check is announced in the log"
else
    fail "--no-verify installed without warning that nothing was verified"
fi
printf '\n'

# --- 4. an asset with no checksum entry is refused ----------------------------
printf '=== asset missing from checksums.txt\n'
show "SAEL_RELEASE_BASE_URL=$BASE/missing SAEL_INSTALL_DIR=$tmp_root/install-missing $INSTALLER --version $VERSION --skip-setup"
missing_dir="$tmp_root/install-missing"
run_install "$missing_dir" "$BASE/missing" "" "$tmp_root/home-missing" --version "$VERSION"
if [ "$rc" -ne 0 ]; then
    pass "the installer refused an unlisted archive (exit $rc)"
else
    fail "the installer exited 0 for an archive absent from checksums.txt"
fi
if printf '%s' "$out" | grep -q 'not listed in checksums.txt'; then
    pass "the refusal names the reason"
else
    fail "the installer failed without saying the asset is unlisted"
    printf '%s\n' "$out"
fi
assert_nothing_installed "$missing_dir"
printf '\n'

# --- 5. an asset that does not exist fails ------------------------------------
printf '=== asset that was never published\n'
show "SAEL_RELEASE_BASE_URL=$BASE/ok SAEL_INSTALL_DIR=$tmp_root/install-absent $INSTALLER --version 9.9.9-nope --skip-setup"
absent_dir="$tmp_root/install-absent"
run_install "$absent_dir" "$BASE/ok" "" "$tmp_root/home-absent" --version "9.9.9-nope"
if [ "$rc" -ne 0 ]; then
    pass "the installer failed on a 404 (exit $rc)"
else
    fail "the installer exited 0 for an archive that does not exist"
fi
if printf '%s' "$out" | grep -q 'Download failed'; then
    pass "the refusal names the reason"
else
    fail "the installer failed without reporting a download failure"
fi
assert_nothing_installed "$absent_dir"
printf '\n'

# --- 6. platforms this release does not build ---------------------------------
#
# uname is stubbed rather than the platform check being trusted: these branches
# are the reason an Intel Mac gets told sael is not built for it instead of being
# handed an arm64 binary that dies with "bad CPU type", and they cannot be reached
# at all on a machine whose own architecture is supported.
printf '=== platforms that are not published\n'
stub_bin="$tmp_root/stub-bin"
mkdir -p "$stub_bin"
cat >"$stub_bin/uname" <<'STUB'
#!/bin/sh
# Answers with whatever SAEL_TEST_UNAME_S / SAEL_TEST_UNAME_M say, so the
# installer's platform detection can be asked for a target no real machine here has.
case "$1" in
    -s) printf '%s\n' "${SAEL_TEST_UNAME_S:-$(/usr/bin/uname -s)}" ;;
    -m) printf '%s\n' "${SAEL_TEST_UNAME_M:-$(/usr/bin/uname -m)}" ;;
    *)  printf '%s\n' "${SAEL_TEST_UNAME_S:-$(/usr/bin/uname -s)}" ;;
esac
STUB
chmod +x "$stub_bin/uname"

unsupported_case() {
    local want_s="$1" want_m="$2" label="$3" expect="$4"
    local slug dir
    slug="$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '-')"
    dir="$tmp_root/install-unsupported-$slug"
    printf -- '-- uname -s %s / uname -m %s\n' "$want_s" "$want_m"
    show "SAEL_TEST_UNAME_S=$want_s SAEL_TEST_UNAME_M=$want_m PATH=$stub_bin:\$PATH SAEL_RELEASE_BASE_URL=$BASE/ok SAEL_INSTALL_DIR=$dir $INSTALLER --skip-setup"
    out=""; rc=0
    if out="$(SAEL_TEST_UNAME_S="$want_s" SAEL_TEST_UNAME_M="$want_m" \
        invoke_install "$dir" "$BASE/ok" "$stub_bin" "$tmp_root/home-$slug" 2>&1)"; then rc=0; else rc=$?; fi
    if [ "$rc" -ne 0 ]; then
        pass "refused (exit $rc)"
    else
        fail "the installer exited 0 for $want_s/$want_m"
    fi
    if printf '%s' "$out" | grep -qF "$expect"; then
        pass "the refusal names the reason: $expect"
    else
        fail "expected the message to mention '$expect'"
        printf '%s\n' "$out"
    fi
    assert_nothing_installed "$dir"
}

unsupported_case Linux  aarch64    linux-arm64   'sael is not built for linux/arm64'
unsupported_case Darwin x86_64     darwin-amd64  'sael is not built for darwin/amd64'
# An architecture nothing here recognises at all: the supported-OS branch has its
# own refusal, and a 32-bit ARM box reporting armv7l is the common way to reach it.
unsupported_case Linux  armv7l     linux-armv7l  'Unsupported architecture: armv7l'
unsupported_case Plan9  amd64      plan9         'Unsupported operating system: Plan9'
# Git Bash on Windows reports a Windows kernel with a POSIX veneer; installing
# linux/amd64 there produces a file Windows will not run.
unsupported_case MINGW64_NT-10.0 amd64 mingw 'Windows detected'
printf '\n'

if [ "$FAILURES" -ne 0 ]; then
    printf '%d check(s) FAILED\n\n' "$FAILURES"
    exit 1
fi
printf 'all checks passed\n\n'
