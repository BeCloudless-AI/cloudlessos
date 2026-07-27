#!/usr/bin/env bash

# Load CloudlessOS release settings from a private per-user file without
# evaluating it as shell code. CLOUDLESS_RELEASE_ENV can override the path.
load_cloudless_release_env() {
    local file="${CLOUDLESS_RELEASE_ENV:-$HOME/.config/cloudless/release.env}"
    [ -f "$file" ] || return 0

    local mode
    mode="$(stat -c '%a' "$file" 2>/dev/null || true)"
    case "$mode" in
        600|400) ;;
        *)
            echo "Release environment file must be private (chmod 600): $file" >&2
            return 1
            ;;
    esac

    local line key value
    while IFS= read -r line || [ -n "$line" ]; do
        line="${line%$'\r'}"
        case "$line" in ""|'#'*) continue ;; esac
        key="${line%%=*}"
        value="${line#*=}"
        [ "$key" != "$line" ] || {
            echo "Invalid release environment entry in $file" >&2
            return 1
        }
        case "$key" in
            CLOUDLESS_ARCHIVE_SECRET|CLOUDLESS_R2_ENDPOINT|CLOUDLESS_R2_BUCKET|AWS_ACCESS_KEY_ID|AWS_SECRET_ACCESS_KEY)
                export "$key=$value"
                ;;
            *)
                echo "Unsupported release environment key '$key' in $file" >&2
                return 1
                ;;
        esac
    done < "$file"
}
