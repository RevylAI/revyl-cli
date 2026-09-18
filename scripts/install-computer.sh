#!/bin/sh
set -eu

fail() {
    printf 'revyl-computer: %s\n' "$1" >&2
    exit 1
}

download() {
    curl --fail --silent --show-error --location \
        --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --connect-timeout 10 --max-time 120 --retry 2 --retry-max-time 240 \
        --output "$2" "$1"
}

setup_path() {
    quoted_dir=$(printf '%s' "$install_dir" | sed "s/'/'\\\\''/g")
    path_line="export PATH='$quoted_dir':\"\$PATH\""
    case "${SHELL##*/}" in
        zsh) profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
        bash)
            if [ -f "$HOME/.bash_profile" ]; then
                profile="$HOME/.bash_profile"
            else
                profile="$HOME/.bashrc"
            fi
            ;;
        fish)
            profile="$HOME/.config/fish/config.fish"
            path_line="set -gx PATH '$quoted_dir' \$PATH"
            ;;
        *) profile="$HOME/.profile" ;;
    esac

    if [ "${REVYL_COMPUTER_NO_MODIFY_PATH:-0}" != "1" ]; then
        if ! { [ -f "$profile" ] && grep -Fqx "$path_line" "$profile"; }; then
            if mkdir -p "$(dirname "$profile")" && printf '\n%s\n' "$path_line" >> "$profile"; then
                printf 'Added revyl-computer to PATH in %s\n' "$profile" >&2
            else
                printf 'Could not update %s; add the PATH line below yourself.\n' "$profile" >&2
            fi
        fi
    fi

    printf 'For this terminal, run:\n  %s\n' "$path_line" >&2
}

main() {
    command -v curl >/dev/null 2>&1 || fail 'curl is required.'
    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        *) fail 'This installer supports macOS and Linux. Download the Windows binary from https://github.com/RevylAI/revyl-cli/releases.' ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) fail 'Unsupported CPU architecture; macOS and Linux builds support amd64 and arm64.' ;;
    esac

    if command -v sha256sum >/dev/null 2>&1; then
        checksum_tool=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        checksum_tool=shasum
    else
        fail 'SHA-256 verification requires sha256sum or shasum.'
    fi

    release_base='https://github.com/RevylAI/revyl-cli/releases'
    version=${REVYL_COMPUTER_VERSION:-}
    if [ -z "$version" ]; then
        release_url=$(curl --fail --silent --show-error --location --head \
            --proto '=https' --proto-redir '=https' --tlsv1.2 \
            --connect-timeout 10 --max-time 30 --retry 2 --retry-max-time 60 \
            --output /dev/null --write-out '%{url_effective}' "$release_base/latest") \
            || fail 'Could not resolve the latest release. Retry or set REVYL_COMPUTER_VERSION to a release tag.'
        case "$release_url" in
            "$release_base/tag/"*) version=${release_url#"$release_base/tag/"} ;;
            *) fail 'The latest release did not resolve to a release tag.' ;;
        esac
    fi
    case "$version" in v*) ;; *) version="v$version" ;; esac
    case "$version" in
        *[!0-9A-Za-z.-]*) fail 'Invalid REVYL_COMPUTER_VERSION; use a release tag such as v0.1.118.' ;;
    esac
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' \
        || fail 'Invalid REVYL_COMPUTER_VERSION; use a release tag such as v0.1.118.'

    install_dir=${REVYL_COMPUTER_INSTALL_DIR:-$HOME/.revyl/bin}
    case "$install_dir" in
        /*) ;;
        *) fail 'REVYL_COMPUTER_INSTALL_DIR must be an absolute path.' ;;
    esac
    case "$install_dir" in
        *'
'*|*:*) fail 'The install directory must not contain newlines or PATH separators.' ;;
    esac
    [ ! -L "$install_dir" ] || fail 'The install directory must not be a symlink.'
    destination="$install_dir/revyl-computer"
    if [ -L "$destination" ] || [ -d "$destination" ]; then
        fail 'The existing revyl-computer path must be a regular file, not a symlink or directory.'
    fi

    mkdir -p "$install_dir"
    staging_dir=$(mktemp -d "$install_dir/.revyl-computer-install.XXXXXX")
    trap 'rm -rf "$staging_dir"' 0
    trap 'exit 1' 1 2 15
    asset="revyl-computer-$os-$arch"
    printf 'Installing revyl-computer %s for %s/%s from %s\n' "$version" "$os" "$arch" "$release_base" >&2
    download "$release_base/download/$version/$asset" "$staging_dir/$asset" \
        || fail 'Binary download failed; check that the requested release contains revyl-computer.'
    download "$release_base/download/$version/checksums.txt" "$staging_dir/checksums.txt" \
        || fail 'Could not download checksums.txt; refusing an unverified install.'

    expected=$(awk -v asset="$asset" '
        $2 == asset || $2 == "*" asset {
            count++
            if (NF != 2 || length($1) != 64 || $1 !~ /^[0-9a-fA-F]+$/) invalid=1
            checksum=tolower($1)
        }
        END {
            if (count != 1 || invalid) exit 1
            print checksum
        }
    ' "$staging_dir/checksums.txt") || fail 'Missing, duplicate, or invalid SHA-256 entry; refusing an unverified install.'
    if [ "$checksum_tool" = sha256sum ]; then
        actual=$(sha256sum "$staging_dir/$asset")
    else
        actual=$(shasum -a 256 "$staging_dir/$asset")
    fi
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; the existing installation was not changed.'

    chmod 755 "$staging_dir/$asset"
    mv -f "$staging_dir/$asset" "$destination"
    printf 'Installed %s\n' "$destination" >&2
    SHELL=${SHELL:-sh}
    setup_path
    printf "Run revyl-computer --help, then use REVYL_API_KEY or your saved 'revyl auth login' credentials to run revyl-computer ssh.\n" >&2
}

main "$@"
