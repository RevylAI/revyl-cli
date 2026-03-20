#!/bin/sh
# Revyl CLI installer
# Usage: curl -fsSL https://raw.githubusercontent.com/RevylAI/revyl-cli/main/scripts/install.sh | sh
#
# Environment variables:
#   REVYL_VERSION       - Pin a specific version (e.g. v0.1.13). Default: latest.
#   REVYL_INSTALL_DIR   - Override install directory. Default: ~/.revyl/bin.
#   REVYL_NO_MODIFY_PATH - Set to 1 to skip PATH modification.

set -e

REPO="RevylAI/revyl-cli"
INSTALL_DIR="${REVYL_INSTALL_DIR:-$HOME/.revyl/bin}"
BINARY_NAME="revyl"

# ── Branding ──────────────────────────────────────────────────────────

show_banner() {
    printf '\n'
    printf '  ╔══════════════════════════════════════════╗\n'
    printf '  ║                                          ║\n'
    printf '  ║   ██████  ███████ ██    ██ ██    ██ ██   ║\n'
    printf '  ║   ██   ██ ██      ██    ██  ██  ██  ██   ║\n'
    printf '  ║   ██████  █████   ██    ██   ████   ██   ║\n'
    printf '  ║   ██   ██ ██       ██  ██     ██    ██   ║\n'
    printf '  ║   ██   ██ ███████   ████      ██    ███  ║\n'
    printf '  ║                                          ║\n'
    printf '  ║   Mobile Reliability                     ║\n'
    printf '  ║                                          ║\n'
    printf '  ╚══════════════════════════════════════════╝\n'
    printf '\n'
}

# ── Helpers ───────────────────────────────────────────────────────────

info()  { printf '  \033[1;34m→\033[0m %s\n' "$1"; }
ok()    { printf '  \033[1;32m✓\033[0m %s\n' "$1"; }
err()   { printf '  \033[1;31m✗\033[0m %s\n' "$1" >&2; }

need_cmd() {
    if ! command -v "$1" > /dev/null 2>&1; then
        err "Required command not found: $1"
        exit 1
    fi
}

# ── Platform detection ────────────────────────────────────────────────

detect_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux)  echo "linux"  ;;
        *)
            err "Unsupported operating system: $(uname -s)"
            err "This installer supports macOS and Linux."
            err "For Windows, use: pip install revyl"
            exit 1
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)
            err "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac
}

# ── Download wrapper (curl preferred, wget fallback) ──────────────────

download() {
    url="$1"
    dest="$2"

    if command -v curl > /dev/null 2>&1; then
        curl -fsSL -o "$dest" "$url"
    elif command -v wget > /dev/null 2>&1; then
        wget -qO "$dest" "$url"
    else
        err "Either curl or wget is required to download files."
        exit 1
    fi
}

# ── Version resolution ────────────────────────────────────────────────

resolve_version() {
    if [ -n "${REVYL_VERSION:-}" ]; then
        echo "$REVYL_VERSION"
        return
    fi

    info "Resolving latest version..."

    if command -v curl > /dev/null 2>&1; then
        tag=$(curl -fsSI "https://github.com/$REPO/releases/latest" 2>/dev/null \
              | grep -i '^location:' | sed 's|.*/||' | tr -d '[:space:]')
    elif command -v wget > /dev/null 2>&1; then
        tag=$(wget --spider -S "https://github.com/$REPO/releases/latest" 2>&1 \
              | grep -i '^\s*location:' | sed 's|.*/||' | tr -d '[:space:]')
    fi

    if [ -z "$tag" ]; then
        err "Could not determine latest version."
        err "Set REVYL_VERSION explicitly (e.g. REVYL_VERSION=v0.1.13)."
        exit 1
    fi

    echo "$tag"
}

# ── Checksum verification ────────────────────────────────────────────

verify_checksum() {
    binary_path="$1"
    asset_name="$2"
    version="$3"

    checksums_url="https://github.com/$REPO/releases/download/$version/checksums.txt"
    tmp_checksums="$(mktemp)"

    info "Verifying checksum..."

    if ! download "$checksums_url" "$tmp_checksums" 2>/dev/null; then
        rm -f "$tmp_checksums"
        info "Checksum file not available for $version, skipping verification."
        return 0
    fi

    expected=$(grep "$asset_name" "$tmp_checksums" | awk '{print $1}')
    rm -f "$tmp_checksums"

    if [ -z "$expected" ]; then
        info "No checksum entry for $asset_name, skipping verification."
        return 0
    fi

    if command -v sha256sum > /dev/null 2>&1; then
        actual=$(sha256sum "$binary_path" | awk '{print $1}')
    elif command -v shasum > /dev/null 2>&1; then
        actual=$(shasum -a 256 "$binary_path" | awk '{print $1}')
    else
        info "No sha256sum or shasum found, skipping verification."
        return 0
    fi

    if [ "$actual" != "$expected" ]; then
        err "Checksum mismatch!"
        err "  Expected: $expected"
        err "  Got:      $actual"
        exit 1
    fi

    ok "Checksum verified"
}

# ── PATH setup ────────────────────────────────────────────────────────

setup_path() {
    install_dir="$1"

    case ":${PATH}:" in
        *":${install_dir}:"*) return ;;
    esac

    if [ "${REVYL_NO_MODIFY_PATH:-}" = "1" ]; then
        printf '\n'
        info "Add the following to your shell profile:"
        printf '    export PATH="%s:$PATH"\n' "$install_dir"
        return
    fi

    line="export PATH=\"${install_dir}:\$PATH\""
    shell_name="$(basename "${SHELL:-sh}")"

    case "$shell_name" in
        zsh)
            rc="$HOME/.zshrc"
            ;;
        bash)
            if [ -f "$HOME/.bash_profile" ]; then
                rc="$HOME/.bash_profile"
            else
                rc="$HOME/.bashrc"
            fi
            ;;
        fish)
            fish_line="set -gx PATH \"${install_dir}\" \$PATH"
            rc="$HOME/.config/fish/config.fish"
            mkdir -p "$(dirname "$rc")"
            if [ -f "$rc" ] && grep -qF "$install_dir" "$rc" 2>/dev/null; then
                return
            fi
            printf '%s\n' "$fish_line" >> "$rc"
            ok "Added $install_dir to PATH in $rc"
            return
            ;;
        *)
            rc="$HOME/.profile"
            ;;
    esac

    if [ -f "$rc" ] && grep -qF "$install_dir" "$rc" 2>/dev/null; then
        return
    fi

    printf '\n%s\n' "$line" >> "$rc"
    ok "Added $install_dir to PATH in $rc"
}

# ── Main ──────────────────────────────────────────────────────────────

main() {
    show_banner

    OS="$(detect_os)"
    ARCH="$(detect_arch)"
    VERSION="$(resolve_version)"
    ASSET="revyl-${OS}-${ARCH}"

    info "Platform: ${OS}/${ARCH}"
    info "Version:  ${VERSION}"

    DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET"

    tmp_dir="$(mktemp -d)"
    tmp_binary="${tmp_dir}/${ASSET}"
    trap 'rm -rf "$tmp_dir"' EXIT

    info "Downloading ${ASSET}..."
    if ! download "$DOWNLOAD_URL" "$tmp_binary"; then
        err "Download failed."
        err "URL: $DOWNLOAD_URL"
        err ""
        err "Check that version $VERSION exists at:"
        err "  https://github.com/$REPO/releases"
        exit 1
    fi
    ok "Downloaded"

    verify_checksum "$tmp_binary" "$ASSET" "$VERSION"

    mkdir -p "$INSTALL_DIR"
    mv "$tmp_binary" "${INSTALL_DIR}/${BINARY_NAME}"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    ok "Installed to ${INSTALL_DIR}/${BINARY_NAME}"

    setup_path "$INSTALL_DIR"

    printf '\n'
    printf '  \033[1;32mRevyl CLI %s installed successfully!\033[0m\n' "$VERSION"
    printf '\n'
    printf '  Next steps:\n'
    printf '    • Restart your shell or run: export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    printf '    • Run: revyl --help\n'
    printf '    • Docs: https://docs.revyl.ai\n'
    printf '\n'
}

main
