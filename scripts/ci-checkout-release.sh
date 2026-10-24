#!/bin/sh
# Select the immutable release source for tag pipelines or explicit recovery runs.
set -eu
[ "$#" -eq 0 ] || { echo 'usage: sh scripts/ci-checkout-release.sh' >&2; exit 2; }
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fail() { echo "error: $*" >&2; exit 1; }
[ "${CI_COMMIT_REF_PROTECTED:-}" = true ] || fail 'release requires a protected CI ref'
[ "${CI_DEBUG_TRACE:-}" != true ] || fail 'disable CI_DEBUG_TRACE before release'
tag=${CI_COMMIT_TAG:-${OBTOP_RELEASE_TAG:-}}
printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'invalid release tag'
if [ -n "${CI_COMMIT_TAG:-}" ]; then
    [ -z "${OBTOP_RELEASE_TAG:-}" ] || [ "$OBTOP_RELEASE_TAG" = "$tag" ] || fail 'conflicting release tags'
else
    [ "${CI_COMMIT_REF_NAME:-}" = main ] || fail 'recovery pipelines must run on protected main'
fi
git fetch origin +refs/heads/main:refs/remotes/origin/main "refs/tags/$tag:refs/tags/$tag"
[ "$(git cat-file -t "refs/tags/$tag")" = tag ] || fail 'release tag must be annotated'
source_commit=$(git rev-parse "refs/tags/$tag^{commit}")
if [ -n "${CI_COMMIT_TAG:-}" ]; then
    [ "$source_commit" = "${CI_COMMIT_SHA:-}" ] || fail 'tag does not match pipeline source'
fi
git merge-base --is-ancestor "$source_commit" origin/main || fail 'release source must belong to main'
git checkout --detach "refs/tags/$tag"
echo "Release source: $tag ($source_commit)"
