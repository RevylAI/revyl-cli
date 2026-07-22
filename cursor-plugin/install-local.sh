#!/bin/sh
#
# Installs this plugin under Cursor's local plugin root.

set -eu

PLUGIN_NAME="revyl"
SOURCE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
LOCAL_PLUGIN_ROOT=${CURSOR_PLUGIN_LOCAL_DIR:-"$HOME/.cursor/plugins/local"}
DESTINATION="$LOCAL_PLUGIN_ROOT/$PLUGIN_NAME"
STAGING="$LOCAL_PLUGIN_ROOT/.${PLUGIN_NAME}.install.$$"
PREVIOUS="$LOCAL_PLUGIN_ROOT/.${PLUGIN_NAME}.previous.$$"
MODE=copy
MODE_SELECTED=false
DRY_RUN=false
STATUS_ONLY=false
LINKED_DIRECTORIES=".cursor-plugin assets hooks rules skills"

# cleanup removes an incomplete staging directory after any installer outcome.
cleanup() {
  rm -rf "$STAGING"
}

# fail reports one actionable installer error.
fail() {
  printf 'Revyl plugin install failed: %s\n' "$1" >&2
  exit 1
}

# usage documents local copy, linked-development, and inspection modes.
usage() {
  cat <<'EOF'
Usage: install-local.sh [--copy | --link] [--dry-run]
       install-local.sh --status

Options:
  --copy      Install an isolated plugin copy (default).
  --link      Link live worktree directories for local development.
              REVYL_BINARY is required.
  --dry-run   Validate and report the planned local change without writing.
  --status    Report the active local plugin without writing.
  -h, --help  Show this help message.
EOF
}

# select_mode rejects ambiguous installation mode arguments.
select_mode() {
  requested_mode=$1
  if [ "$MODE_SELECTED" = true ] && [ "$MODE" != "$requested_mode" ]; then
    fail "--copy and --link cannot be used together"
  fi
  MODE=$requested_mode
  MODE_SELECTED=true
}

# parse_arguments reads the noninteractive local installer options.
parse_arguments() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --copy) select_mode copy ;;
      --link) select_mode link ;;
      --dry-run) DRY_RUN=true ;;
      --status) STATUS_ONLY=true ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        usage >&2
        fail "unknown argument: $1"
        ;;
    esac
    shift
  done

  if [ "$STATUS_ONLY" = true ] &&
    { [ "$DRY_RUN" = true ] || [ "$MODE_SELECTED" = true ]; }; then
    fail "--status cannot be combined with an installation mode or --dry-run"
  fi
}

# resolve_binary returns REVYL_BINARY as a verified absolute executable path.
resolve_binary() {
  requested=$1
  if command -v "$requested" >/dev/null 2>&1; then
    selected=$(command -v "$requested")
  elif [ -f "$requested" ] && [ -x "$requested" ]; then
    selected=$requested
  else
    printf 'Revyl plugin install failed: REVYL_BINARY is not executable: %s\n' "$requested" >&2
    return 1
  fi

  case "$selected" in
    /*) printf '%s\n' "$selected" ;;
    *)
      selected_directory=$(CDPATH= cd -- "$(dirname -- "$selected")" && pwd)
      printf '%s/%s\n' "$selected_directory" "$(basename -- "$selected")"
      ;;
  esac
}

# rewrite_installed_runtime_override changes only the staged binary override.
rewrite_installed_runtime_override() {
  selected_binary=$1
  mcp_path=$2
  rewritten_path="${mcp_path}.tmp"

  REVYL_INSTALLED_BINARY=$selected_binary awk '
    BEGIN {
      replaced = 0
      escaped = ENVIRON["REVYL_INSTALLED_BINARY"]
      gsub(/\\/, "\\\\", escaped)
      gsub(/"/, "\\\"", escaped)
    }
    !replaced && $0 ~ /^[[:space:]]*"REVYL_BINARY"[[:space:]]*:[[:space:]]*"\$\{env:REVYL_BINARY\}"[[:space:]]*,*[[:space:]]*$/ {
      quote_position = index($0, "\"REVYL_BINARY\"")
      indentation = substr($0, 1, quote_position - 1)
      suffix = ($0 ~ /,[[:space:]]*$/) ? "," : ""
      printf "%s\"REVYL_BINARY\": \"%s\"%s\n", indentation, escaped, suffix
      replaced = 1
      next
    }
    { print }
    END {
      if (!replaced) {
        exit 42
      }
    }
  ' "$mcp_path" > "$rewritten_path" || {
    rm -f "$rewritten_path"
    printf '%s\n' "Revyl plugin install failed: mcp.json has no REVYL_BINARY override to rewrite." >&2
    return 1
  }

  mv "$rewritten_path" "$mcp_path"
}

# manifest_name reads the declared plugin name from one manifest.
manifest_name() {
  manifest_path=$1
  sed -n '
    /^[[:space:]]*"name"[[:space:]]*:[[:space:]]*"[^"]*"[[:space:]]*,*[[:space:]]*$/ {
      s/^[[:space:]]*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)"[[:space:]]*,*[[:space:]]*$/\1/
      p
      q
    }
  ' "$manifest_path"
}

# validate_source verifies the maintained artifact before local mutation.
validate_source() {
  source_manifest="$SOURCE_DIR/.cursor-plugin/plugin.json"
  [ -f "$source_manifest" ] ||
    fail "source artifact has no .cursor-plugin/plugin.json"
  [ "$(manifest_name "$source_manifest")" = "$PLUGIN_NAME" ] ||
    fail "source manifest does not declare the revyl plugin"
  [ -f "$SOURCE_DIR/mcp.json" ] ||
    fail "source artifact has no mcp.json"
}

# installed_runtime_override reads the local MCP binary selection.
installed_runtime_override() {
  mcp_path=$1
  sed -n '
    /^[[:space:]]*"REVYL_BINARY"[[:space:]]*:[[:space:]]*"[^"]*"[[:space:]]*,*[[:space:]]*$/ {
      s/^[[:space:]]*"REVYL_BINARY"[[:space:]]*:[[:space:]]*"\([^"]*\)"[[:space:]]*,*[[:space:]]*$/\1/
      p
      q
    }
  ' "$mcp_path"
}

# print_status reports the active local install without changing it.
print_status() {
  printf 'Revyl local plugin status\n'
  printf '  Destination: %s\n' "$DESTINATION"
  if [ ! -d "$DESTINATION" ]; then
    printf '%s\n' "  Installed: no"
    return
  fi

  installed_mode=copy
  installed_source="copied artifact"
  if [ -L "$DESTINATION" ]; then
    installed_mode=link
    installed_source=$(readlink "$DESTINATION")
  elif [ -L "$DESTINATION/.cursor-plugin" ]; then
    installed_mode=link
    manifest_target=$(readlink "$DESTINATION/.cursor-plugin")
    installed_source=${manifest_target%/.cursor-plugin}
  fi

  runtime_override=unavailable
  if [ -f "$DESTINATION/mcp.json" ]; then
    detected_override=$(installed_runtime_override "$DESTINATION/mcp.json")
    if [ -n "$detected_override" ]; then
      runtime_override=$detected_override
    fi
  fi

  printf '%s\n' "  Installed: yes"
  printf '  Mode: %s\n' "$installed_mode"
  printf '  Source: %s\n' "$installed_source"
  printf '  Revyl binary: %s\n' "$runtime_override"
}

# link_worktree_directories replaces copied development surfaces with live links.
link_worktree_directories() {
  for relative_path in $LINKED_DIRECTORIES; do
    rm -rf "$STAGING/$relative_path"
    ln -s "$SOURCE_DIR/$relative_path" "$STAGING/$relative_path"
  done
}

# replace_destination activates the staged plugin and restores the prior install on failure.
replace_destination() {
  had_previous=false
  rm -rf "$PREVIOUS"
  if [ -e "$DESTINATION" ] || [ -L "$DESTINATION" ]; then
    mv "$DESTINATION" "$PREVIOUS"
    had_previous=true
  fi
  if mv "$STAGING" "$DESTINATION"; then
    rm -rf "$PREVIOUS"
    return
  fi
  if [ "$had_previous" = true ]; then
    mv "$PREVIOUS" "$DESTINATION" || true
  fi
  fail "could not activate the staged plugin"
}

parse_arguments "$@"

if [ "$STATUS_ONLY" = true ]; then
  print_status
  exit 0
fi

validate_source

SELECTED_BINARY=
if [ -n "${REVYL_BINARY:-}" ]; then
  SELECTED_BINARY=$(resolve_binary "$REVYL_BINARY")
elif [ "$MODE" = link ]; then
  fail "REVYL_BINARY is required for --link"
fi

if [ "$DRY_RUN" = true ]; then
  printf '%s\n' "Revyl local plugin dry run"
  printf '  Mode: %s\n' "$MODE"
  printf '  Source: %s\n' "$SOURCE_DIR"
  printf '  Destination: %s\n' "$DESTINATION"
  if [ -n "$SELECTED_BINARY" ]; then
    printf '  Revyl binary: %s\n' "$SELECTED_BINARY"
  else
    printf '%s\n' "  Revyl binary: plugin-pinned runtime"
  fi
  printf '%s\n' "  Changes: none"
  exit 0
fi

trap cleanup EXIT HUP INT TERM
mkdir -p "$LOCAL_PLUGIN_ROOT"
rm -rf "$STAGING"
mkdir -p "$STAGING"
cp -R "$SOURCE_DIR/." "$STAGING/"

if [ "$MODE" = link ]; then
  link_worktree_directories
fi

MANIFEST_PATH="$STAGING/.cursor-plugin/plugin.json"
if [ ! -f "$MANIFEST_PATH" ]; then
  fail "staged artifact has no .cursor-plugin/plugin.json"
fi

MANIFEST_NAME=$(manifest_name "$MANIFEST_PATH")
if [ "$MANIFEST_NAME" != "$PLUGIN_NAME" ]; then
  fail "staged manifest does not declare the revyl plugin"
fi

if [ -n "$SELECTED_BINARY" ]; then
  rewrite_installed_runtime_override "$SELECTED_BINARY" "$STAGING/mcp.json"
fi

replace_destination
trap - EXIT HUP INT TERM

if [ "$MODE" = link ]; then
  printf 'Linked Revyl plugin from %s\n' "$SOURCE_DIR"
else
  printf 'Installed Revyl plugin copy from %s\n' "$SOURCE_DIR"
fi
printf 'Local plugin destination: %s\n' "$DESTINATION"
printf '%s\n' "In Cursor, run: Developer: Reload Window"
