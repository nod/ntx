#!/bin/sh
# ntx installer
# Usage: curl -sSf https://raw.githubusercontent.com/nerdtx/ntx/main/install.sh | sh
#
# Environment variables:
#   NTX_VERSION   — version to install (default: latest)
#   INSTALL_DIR   — install location (default: $HOME/.local/bin)

set -e

REPO="nerdtx/ntx"
VERSION="${NTX_VERSION:-}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ── helpers ──────────────────────────────────────────────────────────────────

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m✓\033[0m  %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m  %s\n' "$*" >&2; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"
}

# ── platform detection ────────────────────────────────────────────────────────

detect_os() {
    case "$(uname -s)" in
        Linux)  echo "linux" ;;
        Darwin) echo "darwin" ;;
        *)      die "unsupported OS: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64)          echo "amd64" ;;
        aarch64|arm64)   echo "arm64" ;;
        *)               die "unsupported architecture: $(uname -m)" ;;
    esac
}

# ── download helpers ──────────────────────────────────────────────────────────

http_get() {
    url="$1"
    dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -sSfL "$url" -o "$dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dest" "$url"
    else
        die "curl or wget is required"
    fi
}

latest_version() {
    tmp=$(mktemp)
    if ! http_get "https://api.github.com/repos/$REPO/releases/latest" "$tmp" 2>/dev/null; then
        rm -f "$tmp"
        return 1
    fi
    ver=$(grep '"tag_name"' "$tmp" | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    rm -f "$tmp"
    [ -n "$ver" ] && echo "$ver" || return 1
}

# ── install from release binary ───────────────────────────────────────────────

install_binary() {
    os="$1"
    arch="$2"
    ver="$3"

    name="ntx_${os}_${arch}"
    url="https://github.com/$REPO/releases/download/$ver/$name"
    tmp=$(mktemp)

    say "downloading ntx $ver ($os/$arch)..."
    if ! http_get "$url" "$tmp" 2>/dev/null; then
        rm -f "$tmp"
        return 1
    fi

    chmod +x "$tmp"

    # sanity-check the binary runs
    if ! "$tmp" --help >/dev/null 2>&1; then
        rm -f "$tmp"
        warn "downloaded binary failed sanity check"
        return 1
    fi

    mkdir -p "$INSTALL_DIR"
    mv "$tmp" "$INSTALL_DIR/ntx"
    ok "installed ntx $ver → $INSTALL_DIR/ntx"
    return 0
}

# ── install from source via go install ───────────────────────────────────────

install_from_source() {
    ver="$1"
    if ! command -v go >/dev/null 2>&1; then
        return 1
    fi

    pkg="github.com/$REPO"
    if [ -n "$ver" ] && [ "$ver" != "latest" ]; then
        pkg="${pkg}@${ver}"
    else
        pkg="${pkg}@latest"
    fi

    say "building from source: go install $pkg"
    GOBIN="$INSTALL_DIR" go install "$pkg" || return 1
    ok "installed ntx → $INSTALL_DIR/ntx"
    return 0
}

# ── path check ────────────────────────────────────────────────────────────────

check_path() {
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
            echo ""
            warn "$INSTALL_DIR is not in your PATH"
            warn "add this to your shell profile:"
            printf '    export PATH="%s:$PATH"\n' "$INSTALL_DIR"
            ;;
    esac
}

# ── main ──────────────────────────────────────────────────────────────────────

main() {
    os=$(detect_os)
    arch=$(detect_arch)

    # resolve version
    if [ -z "$VERSION" ]; then
        say "checking latest release..."
        VERSION=$(latest_version 2>/dev/null || true)
        if [ -z "$VERSION" ]; then
            warn "could not fetch latest version from GitHub; will build from source"
        fi
    fi

    # try pre-built binary first, then fall back to source
    if [ -n "$VERSION" ] && install_binary "$os" "$arch" "$VERSION"; then
        :
    elif install_from_source "$VERSION"; then
        :
    else
        die "installation failed: no pre-built binary available and 'go' not found.
  Install Go from https://go.dev/dl/ and re-run this script, or build manually:
    git clone https://github.com/$REPO
    cd ntx && go build -o ~/.local/bin/ntx ."
    fi

    check_path

    echo ""
    say "ntx is ready. next steps:"
    printf '  ntx gen-config > ntx.toml   # generate a starter config\n'
    printf '  ntx hash-password            # generate admin password hash\n'
    printf '  ntx serve                    # start the server\n'
}

main
