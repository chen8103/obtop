#!/bin/sh
# Local-only build and packaging. Never creates tags or publishes artifacts.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
GO=${GO:-go}
action=${1:-}
dry_run=false
if [ "$#" -eq 2 ] && [ "$2" = --dry-run ]; then dry_run=true
elif [ "$#" -ne 1 ]; then
    echo 'usage: sh scripts/build.sh {build|release|clean} [--dry-run]' >&2; exit 2
fi
case "$action" in build|release|clean) ;; *) echo 'invalid action' >&2; exit 2;; esac
fail() { echo "error: $*" >&2; exit 1; }
if [ "$action" = clean ]; then
    echo 'Remove only build/ and dist/'
    if [ "$dry_run" = false ]; then rm -rf build dist; fi
    exit 0
fi
version=dev
commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
if [ "$action" = release ]; then
    [ -z "$(git status --porcelain --untracked-files=normal)" ] || fail 'release requires a clean worktree (including untracked source files)'
    tags=$(git tag --points-at HEAD)
    [ "$(printf '%s\n' "$tags" | sed '/^$/d' | wc -l | tr -d ' ')" = 1 ] || fail 'HEAD must have exactly one release tag'
    version=$tags
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'tag must be vMAJOR.MINOR.PATCH'
    [ "$(git cat-file -t "refs/tags/$version")" = tag ] || fail 'release tag must be annotated'
    [ ! -e "dist/$version" ] || fail "dist/$version already exists; inspect it before make clean"
fi
echo "$action: $version ($commit)"
[ "$dry_run" = false ] || exit 0
build_time=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
flags="-X main.appVersion=$version -X main.gitCommit=$commit -X main.buildTime=$build_time"
if [ "$action" = build ]; then
    mkdir -p build
    CGO_ENABLED=0 "$GO" build -trimpath -ldflags "$flags" -o build/obtop ./cmd/obtop
    exit 0
fi
# Ordered gates: no packaging is attempted until all checks pass.
"$GO" test ./...
"$GO" vet ./...
CGO_ENABLED=1 "$GO" test -race ./...
# Stage privately and publish the directory only after all artifacts are ready.
mkdir -p dist
stage=$(mktemp -d dist/.release.XXXXXX)
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM
(cd examples/consumer && CGO_ENABLED=0 "$GO" build -trimpath -o "../../$stage/consumer" .)
rm "$stage/consumer"
for platform in linux_amd64 darwin_arm64; do
    os=${platform%_*}
    arch=${platform#*_}
    mkdir "$stage/$platform"
    echo "Building $os/$arch"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$GO" build -trimpath -ldflags "$flags" -o "$stage/$platform/obtop" ./cmd/obtop
    if [ "$("$GO" env GOHOSTOS)" = "$os" ] && [ "$("$GO" env GOHOSTARCH)" = "$arch" ]; then
        "$stage/$platform/obtop" version
        "$stage/$platform/obtop" --help >/dev/null 2>&1
    fi
    cp README.md "$stage/$platform/README.md"
    COPYFILE_DISABLE=1 tar -czf "$stage/obtop_${version}_${platform}.tar.gz" -C "$stage/$platform" obtop README.md
    rm -rf "$stage/$platform"
done
(cd "$stage" && shasum -a 256 ./*.tar.gz > SHA256SUMS && shasum -a 256 -c SHA256SUMS)
printf '%s\n' "$commit" > "$stage/SOURCE_COMMIT"
mv "$stage" "dist/$version"
echo "Release artifacts ready: dist/$version"
