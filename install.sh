#!/usr/bin/env bash
#
# Installer for sael on Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/cipherTing/sael/main/install.sh | bash
#
# sael is one static Go binary with no runtime dependencies, so this script does
# three things: fetch a release archive, prove it is the archive the release
# published, and put the binary where an unprivileged user can write. The Hermes
# installer this UX was modelled on contributes its prompts and nothing else --
# it is ~110 KB because it also provisions Python, uv and Node, and there is no
# equivalent here to provision.
#
# Two properties are deliberate:
#
#   1. The SHA-256 is always checked against the release's checksums.txt. An
#      installer people run through `curl | bash` without reading it is the
#      highest-value place in the project to plant a bad binary, and "we trust
#      GitHub" is not a property of the bytes on disk. --no-verify exists for a
#      release whose checksums file is itself broken, and it shouts.
#   2. Nothing is installed with sudo and nothing is written to a system
#      directory. A relay is run by whoever owns the machine, and an installer
#      that needs root cannot be run on the box it is meant for.
#
# Written for `bash`, not `sh`: it uses `local` and `<<<`, which dash lacks.
# macOS still ships bash 3.2 at /bin/bash, so no bash 4 syntax either -- no
# ${var,,}, no mapfile, no associative arrays. A script that only runs under the
# Homebrew bash fails on the machine it is installing to.

set -euo pipefail

REPO="cipherTing/sael"
RELEASES_URL="https://github.com/${REPO}/releases"
# Two modules (sdk/ and cli/) are tagged in their own namespaces, because a
# subdirectory module's tag must carry the directory name as a prefix. The CLI
# tag is `cli/v0.0.1-rc1`, not `v0.0.1-rc1`; the download path must match it or
# every fetch 404s.
TAG_PREFIX="cli/"
# The published matrix, and the only thing the platform check accepts. Intel
# macOS and linux/arm64 are absent because goreleaser builds what CI builds; a
# user handed the wrong architecture gets a binary that dies with "bad CPU type"
# long after the install reported success.
SUPPORTED_PLATFORMS="darwin/arm64 linux/amd64 windows/amd64"

VERSION=""
SKIP_SETUP=false
VERIFY=true

# Defaults, named so that resolve_paths can refer to them without repeating them.
DEFAULT_INSTALL_ROOT="${HOME}/.local/share/sael"
DEFAULT_BIN_DIR="${HOME}/.local/bin"

# Where the binary goes, and where the `sael` symlink goes. Both are left empty
# until resolve_paths runs, because the answer depends on which flags were given
# -- and that is not knowable here. An empty value means "nobody asked": the
# flags are parsed further down, and the environment is the other source.
INSTALL_ROOT="${SAEL_INSTALL_DIR:-}"
BIN_DIR="${SAEL_BIN_DIR:-}"
BASE_URL="${SAEL_RELEASE_BASE_URL:-}"

OS=""
ARCH=""
TMP_DIR=""

# Colours are dropped when stdout is not a terminal or NO_COLOR is set, so a CI
# log is not full of escapes. `curl | bash` still gets colour: bash's stdout is
# the terminal even though its stdin is the pipe.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    BOLD=$'\033[1m'; RED=$'\033[31m'; GREEN=$'\033[32m'
    YELLOW=$'\033[33m'; CYAN=$'\033[36m'; NC=$'\033[0m'
else
    BOLD=""; RED=""; GREEN=""; YELLOW=""; CYAN=""; NC=""
fi

log_info()    { printf '%s %s\n' "${CYAN}->${NC}" "$*"; }
log_success() { printf '%s %s\n' "${GREEN}ok${NC}" "$*"; }
log_warn()    { printf '%s %s\n' "${YELLOW}!!${NC}" "$*" >&2; }
log_error()   { printf '%s %s\n' "${RED}xx${NC}" "$*" >&2; }
die()         { log_error "$*"; exit 1; }

usage() {
    cat <<'EOF'
Install sael from a GitHub Release. Linux and macOS.

Usage: install.sh [OPTIONS]

  --version <v>         Install this version instead of the latest release;
                        the bare number, as in --version 0.0.1-rc1
  --install-dir <path>  Root holding the per-version directories
                        (default ~/.local/share/sael)
  --bin-dir <path>      Where the `sael` symlink is created (default
                        ~/.local/bin, or <install-dir>/bin alongside --install-dir)
  --skip-setup          Install only; skip the `sael setup` onboarding
  --no-verify           Do not check the archive against checksums.txt; only for
                        a release whose checksums file is itself broken
  -h, --help            Show this help

Environment:
  SAEL_RELEASE_BASE_URL  Fetch archives from here instead of GitHub. Replaces
                         the whole base URL, so it must already include the
                         version -- pass --version alongside it.
  SAEL_INSTALL_DIR       Same as --install-dir.
  SAEL_BIN_DIR           Same as --bin-dir.
  SAEL_HOME              Config directory for `sael setup` (default ~/.Sael);
                         passed straight through.

The `sael` command is installed user-scoped. sudo is never used.
EOF
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --version|-Version)
                [ $# -ge 2 ] || die "$1 needs a value"
                VERSION="$2"; shift 2 ;;
            --install-dir|--dir|-InstallDir)
                [ $# -ge 2 ] || die "$1 needs a value"
                INSTALL_ROOT="$2"; shift 2 ;;
            --bin-dir|-BinDir)
                [ $# -ge 2 ] || die "$1 needs a value"
                BIN_DIR="$2"; shift 2 ;;
            --skip-setup|-SkipSetup) SKIP_SETUP=true; shift ;;
            --no-verify|-NoVerify)   VERIFY=false; shift ;;
            -h|--help)               usage; exit 0 ;;
            *) log_error "Unknown option: $1"; printf '\n'; usage; exit 1 ;;
        esac
    done

    # Accept "v0.0.1-rc1" and "cli/v0.0.1-rc1" as well as the bare number, so
    # pasting the tag off the releases page does the obvious thing instead of
    # 404ing on an asset name the user cannot see is wrong.
    case "$VERSION" in
        "${TAG_PREFIX}"v*) VERSION="${VERSION#"${TAG_PREFIX}"v}" ;;
        v*) VERSION="${VERSION#v}" ;;
    esac
}

cleanup() { if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi; }
trap cleanup EXIT INT TERM

# Decide the two directories, after both the flags and the environment have been
# read. This cannot be done where the variables are declared: --install-dir is
# parsed afterwards, and a default computed at declaration time is exactly how
# the flag form came to put the binary under <install-dir> while writing its
# symlink into the user's real ~/.local/bin.
#
# Precedence, highest first:
#   --bin-dir / SAEL_BIN_DIR          the symlink directory, named outright
#   --install-dir / SAEL_INSTALL_DIR  a root that takes the symlink with it
#   the built-in defaults
resolve_paths() {
    if [ -n "$INSTALL_ROOT" ]; then
        # A relocated root takes the symlink with it. --install-dir is the
        # isolated-install form, and an isolated install that reaches out and
        # repoints the user's existing ~/.local/bin/sael at a throwaway tree is
        # worse than no isolation at all. --bin-dir still wins when it was given.
        if [ -z "$BIN_DIR" ]; then BIN_DIR="${INSTALL_ROOT}/bin"; fi
    else
        INSTALL_ROOT="$DEFAULT_INSTALL_ROOT"
        if [ -z "$BIN_DIR" ]; then BIN_DIR="$DEFAULT_BIN_DIR"; fi
    fi
}

detect_platform() {
    local uname_s uname_m
    uname_s="$(uname -s)"
    uname_m="$(uname -m)"

    case "$uname_s" in
        Darwin) OS="darwin" ;;
        Linux)  OS="linux" ;;
        # Git Bash, MSYS2 and Cygwin report a Windows kernel with a POSIX veneer.
        # Installing linux/amd64 here yields a file Windows refuses to run, so hand
        # over the one-liner that works rather than half-install.
        CYGWIN*|MINGW*|MSYS*)
            log_error "Windows detected ($uname_s). This is the Linux/macOS installer."
            log_info "On Windows, use the PowerShell installer:"
            printf '\n   iex (irm https://raw.githubusercontent.com/%s/main/install.ps1)\n\n' "$REPO"
            exit 1 ;;
        *) die "Unsupported operating system: $uname_s. Built for: $SUPPORTED_PLATFORMS" ;;
    esac

    case "$uname_m" in
        x86_64|amd64)  ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *) die "Unsupported architecture: $uname_m. Built for: $SUPPORTED_PLATFORMS" ;;
    esac

    case "$OS/$ARCH" in
        darwin/arm64|linux/amd64) ;;
        *)
            # Refusing is the point. Installing the nearest thing that does exist is
            # how an Intel Mac ends up with an arm64 binary and a "bad CPU type"
            # error three steps later, with no hint of where it came from.
            log_error "sael is not built for $OS/$ARCH."
            log_info "This release publishes:"
            printf '   darwin/arm64    (Apple silicon)\n   linux/amd64     (x86-64)\n   windows/amd64   (use install.ps1)\n\n'
            log_info "Or build it from source:"
            printf '   git clone https://github.com/%s && cd sael/cli && go build -o sael ./cmd/sael\n\n' "$REPO"
            exit 1 ;;
    esac
}

# Resolve a release tag without jq: jq is not on a stock macOS or a slim Linux
# image, and adding a package-manager step to read one string is a worse trade
# than parsing it here.
resolve_version() {
    if [ -n "$VERSION" ]; then return; fi

    # A mirror (CI, or a maintainer testing a snapshot) has no version to offer, so
    # name the directory after what it stands in for rather than reaching out to
    # GitHub from inside the test that redirected it here.
    if [ -n "$BASE_URL" ]; then VERSION="0.0.0-local"; return; fi

    local url tag=""
    # One request against /releases/latest, then read where it landed: no JSON at
    # all. It cannot see a prerelease, and the first tag this project publishes is
    # 0.0.1-rc1, so fall through to the API list -- which does include prereleases
    # -- when the redirect does not name a tag.
    url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${RELEASES_URL}/latest" 2>/dev/null || true)"
    case "$url" in
        */releases/tag/*) tag="${url##*/releases/tag/}" ;;
    esac
    if [ -z "$tag" ]; then
        tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=1" 2>/dev/null \
            | tr ',' '\n' \
            | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
            | head -n 1)"
    fi
    if [ -z "$tag" ]; then
        log_error "Could not determine the latest release of ${REPO}."
        log_info "Name it explicitly:  install.sh --version 0.0.1-rc1"
        exit 1
    fi

    case "$tag" in
        "${TAG_PREFIX}"v*) VERSION="${tag#"${TAG_PREFIX}"v}" ;;
        v*) VERSION="${tag#v}" ;;
        *) VERSION="$tag" ;;
    esac
}

sha256_of() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$1" | awk '{print $NF}'
    else
        die "No SHA-256 tool found. Install one of: shasum, sha256sum, openssl."
    fi
}

fetch_and_verify() {
    local asset="$1" expected actual
    TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sael-install.XXXXXX")"

    log_info "Downloading ${asset}"
    if ! curl -fsSL --retry 3 --retry-delay 2 -o "${TMP_DIR}/${asset}" "${BASE_URL}/${asset}"; then
        log_error "Download failed: ${BASE_URL}/${asset}"
        log_info "If that release does not exist yet, list what does:"
        printf '   %s\n\n' "$RELEASES_URL"
        exit 1
    fi

    # Not a quiet warning: the script has just installed bytes it cannot account
    # for, and a pasted install log is how that gets caught.
    if [ "$VERIFY" = false ]; then
        printf '\n'
        log_warn "============================================================="
        log_warn "--no-verify: the archive was NOT checked against checksums.txt."
        log_warn "Nothing here proves ${asset} is what the release published."
        log_warn "============================================================="
        printf '\n'
        return
    fi

    log_info "Verifying ${asset} against checksums.txt"
    if ! curl -fsSL --retry 3 --retry-delay 2 -o "${TMP_DIR}/checksums.txt" "${BASE_URL}/checksums.txt"; then
        log_error "Could not download ${BASE_URL}/checksums.txt"
        log_info "Without it the archive cannot be verified. --no-verify skips the"
        log_info "check entirely; use it only if you know why the file is missing."
        exit 1
    fi

    # goreleaser writes "<hash>  <name>", and the name carries a leading '*' when
    # the hash was produced in binary mode, so strip that before comparing.
    expected="$(awk -v want="$asset" '
        { name = $2; sub(/^\*/, "", name); if (name == want) { print $1; exit } }
    ' "${TMP_DIR}/checksums.txt")"
    if [ -z "$expected" ]; then
        log_error "${asset} is not listed in checksums.txt."
        log_info "A release that does not vouch for its own asset cannot be verified."
        exit 1
    fi

    actual="$(sha256_of "${TMP_DIR}/${asset}")"
    # Compared case-insensitively: goreleaser lowercases what it writes, and a
    # hand-maintained checksums file should not be rejected over capitalisation.
    if [ "$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" \
        != "$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')" ]; then
        printf '\n'
        log_error "Checksum mismatch for ${asset}."
        log_error "  expected: ${expected}"
        log_error "  actual:   ${actual}"
        log_info "The download is not the published file. Nothing was installed."
        exit 1
    fi
    log_success "SHA-256 verified: ${actual}"
}

install_binary() {
    local asset="$1" version_dir="${INSTALL_ROOT}/${VERSION}"

    mkdir -p "${TMP_DIR}/extract"
    if ! tar -xzf "${TMP_DIR}/${asset}" -C "${TMP_DIR}/extract"; then
        log_error "Could not extract ${asset}."
        exit 1
    fi
    if [ ! -f "${TMP_DIR}/extract/sael" ]; then
        log_error "${asset} has no file named 'sael' at its root."
        log_info "Found instead:"
        (cd "${TMP_DIR}/extract" && ls -1) >&2 || true
        exit 1
    fi

    if [ -d "$version_dir" ]; then log_info "Replacing the existing ${VERSION} install"; fi
    mkdir -p "$version_dir"
    # Staged beside the destination and renamed into place: rename is atomic on one
    # filesystem, so a killed installer cannot leave a truncated binary where the
    # symlink points. It also replaces a running binary safely, which a
    # truncate-in-place copy would not.
    cp "${TMP_DIR}/extract/sael" "${version_dir}/.sael.tmp.$$"
    chmod 0755 "${version_dir}/.sael.tmp.$$"
    mv -f "${version_dir}/.sael.tmp.$$" "${version_dir}/sael"
    log_success "Installed ${version_dir}/sael"

    mkdir -p "$BIN_DIR"
    if [ -d "${BIN_DIR}/sael" ] && [ ! -L "${BIN_DIR}/sael" ]; then
        log_error "${BIN_DIR}/sael is a directory, not a symlink."
        log_info "Move it aside and re-run; this installer will not delete it for you."
        exit 1
    fi
    # Same staged rename on the symlink, so `sael` is never briefly absent for a
    # process about to exec it. Re-running replaces the old link, which is what
    # keeps exactly one sael on PATH after an upgrade.
    ln -sfn "${version_dir}/sael" "${BIN_DIR}/.sael.tmp.$$"
    mv -f "${BIN_DIR}/.sael.tmp.$$" "${BIN_DIR}/sael"
    log_success "Linked ${BIN_DIR}/sael -> ${version_dir}/sael"
}

# Print the line that makes the current shell see the CLI. Guessing the wrong rc
# file here is the difference between "installed" and "command not found".
reload_hint() {
    case "$(basename "${SHELL:-/bin/bash}")" in
        zsh)  printf 'source ~/.zshrc\n' ;;
        bash) printf 'source ~/.bashrc\n' ;;
        fish) printf 'source ~/.config/fish/config.fish\n' ;;
        *)    printf 'source ~/.bashrc   # or ~/.zshrc\n' ;;
    esac
}

warn_if_not_on_path() {
    case ":${PATH}:" in
        *":${BIN_DIR}:"*) return ;;
    esac
    log_warn "${BIN_DIR} is not on your PATH, so 'sael' will not be found yet."
    log_info "Add it for this shell with:"
    # The user is handed a line to paste, so '$PATH' has to survive as a literal
    # rather than expanding here into this script's own PATH.
    # shellcheck disable=SC2016
    printf '   export PATH="%s:$PATH"\n\n' "$BIN_DIR"
}

run_setup() {
    local exe="${BIN_DIR}/sael" rc=0

    if [ "$SKIP_SETUP" = true ]; then
        log_info "Skipping configuration (--skip-setup)."
        log_info "Run '${exe} setup' whenever you want to configure it."
        return 0
    fi

    # The wizard needs a terminal, and `curl | bash` makes stdin the script itself,
    # so `-t 0` is false on a machine with a perfectly good console. The prompts
    # therefore come from /dev/tty, the controlling terminal, whatever stdin was
    # redirected from.
    #
    # Probe it by OPENING it rather than testing that the node exists: in a
    # container the device node is in the mount namespace so `[ -e ]` passes, but
    # open(2) fails with ENXIO and the wizard launches only to die on its first
    # read. `(: </dev/tty)` performs the open and discards the result, which is the
    # only probe that answers the question actually asked.
    if [ -t 0 ]; then
        "$exe" setup || rc=$?
    elif (: </dev/tty) 2>/dev/null; then
        "$exe" setup < /dev/tty || rc=$?
    else
        log_warn "No terminal available, so configuration was skipped."
        log_info "sael is installed and will run, but it is not configured."
        log_info "Run '${exe} setup' from an interactive shell."
        return 0
    fi

    if [ "$rc" -ne 0 ]; then
        # The status is passed on, not swallowed. An install whose onboarding failed
        # is not a successful install, and reporting one makes the exit code useless
        # to whoever is scripting this.
        printf '\n'
        log_error "sael setup exited with status ${rc}."
        log_error "sael ${VERSION} is installed at ${INSTALL_ROOT}/${VERSION}/sael"
        log_error "but is NOT configured, and will fail until it is."
        log_info "Re-run '${exe} setup' to try again."
        return "$rc"
    fi
    log_success "Configuration complete."
}

main() {
    parse_args "$@"
    resolve_paths

    printf '\n%s\n' "${BOLD}${CYAN}sael installer${NC}"
    printf '%s\n\n' "Measure a request's content against a moderation question set."

    detect_platform
    resolve_version
    if [ -z "$BASE_URL" ]; then
        BASE_URL="${RELEASES_URL}/download/${TAG_PREFIX}v${VERSION}"
    fi
    local asset="sael_${VERSION}_${OS}_${ARCH}.tar.gz"

    log_info "Platform:  ${OS}/${ARCH}"
    log_info "Version:   ${VERSION}"
    log_info "Source:    ${BASE_URL}"
    printf '\n'

    fetch_and_verify "$asset"
    install_binary "$asset"
    warn_if_not_on_path

    # `run_setup`'s status is carried all the way out. A script that prints
    # "installed but unconfigured" and then exits 0 tells whoever is scripting
    # this the opposite of what happened, and drops the one signal that says the
    # onboarding needs another look.
    local setup_rc=0
    run_setup || setup_rc=$?

    printf '\n'
    if [ "$setup_rc" -ne 0 ]; then
        log_warn "sael ${VERSION} is installed but unconfigured."
    else
        log_success "sael ${VERSION} is installed."
    fi

    printf '\n'
    log_info "Commands:"
    printf '   sael --help        Show what sael can do\n'
    printf '   sael setup         Configure the API endpoint, model and key\n\n'
    log_info "Reload your shell to use it here:"
    printf '   %s\n\n' "$(reload_hint)"

    return "$setup_rc"
}

main "$@"
