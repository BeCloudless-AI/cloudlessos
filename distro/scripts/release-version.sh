#!/usr/bin/env bash
# Shared CloudlessOS release-version policy.
#
# Published versions use Debian's native upstream-revision form:
#   0.2.7-1, 0.2.7-2, ...
# Emergency rebuilds may append a lowercase hotfix letter:
#   0.2.7-9 < 0.2.7-9a < 0.2.7-10
# Development trees append ~dev so they sort before the corresponding release:
#   0.2.7-1~dev < 0.2.7-1

CLOUDLESS_RELEASE_VERSION_RE='^[0-9]+\.[0-9]+\.[0-9]+(-[1-9][0-9]*[a-z]?)?$'

cloudless_is_release_version() {
    [[ "${1:-}" =~ $CLOUDLESS_RELEASE_VERSION_RE ]]
}

cloudless_release_version_from_source() {
    local version="${1:-}"
    printf '%s\n' "${version%~dev}"
}

cloudless_is_source_version() {
    local release_version
    release_version="$(cloudless_release_version_from_source "${1:-}")"
    cloudless_is_release_version "$release_version" &&
        { [ "${1:-}" = "$release_version" ] || [ "${1:-}" = "$release_version~dev" ]; }
}
