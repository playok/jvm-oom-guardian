#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-}
if [ -z "$VERSION" ]; then
  echo "usage: $0 VERSION" >&2
  echo "example: $0 v1.0.0" >&2
  exit 2
fi
case "$VERSION" in
  v*[!A-Za-z0-9._-]*) echo "VERSION contains unsupported characters" >&2; exit 2 ;;
  v*) ;;
  *) echo "VERSION must start with v (for example v1.0.0)" >&2; exit 2 ;;
esac

DIST_DIR="$ROOT_DIR/dist/$VERSION"
GOCACHE=${GOCACHE:-"$ROOT_DIR/.cache/go-build"}
export GOCACHE
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

build_one() {
  target_os=$1
  target_arch=$2
  archive_name="$BINARY_NAME-$VERSION-$target_os-$target_arch"
  stage_dir="$DIST_DIR/$archive_name"
  mkdir -p "$stage_dir"
  echo "building $target_os/$target_arch"
  (
    cd "$ROOT_DIR"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
      go build -trimpath -ldflags="-s -w" \
      -o "$stage_dir/jvm-oom-guardian" .
  )
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/config.example.json" \
    "$ROOT_DIR/jvm-oom-guardian.service.example" "$stage_dir/"
  chmod 0755 "$stage_dir/jvm-oom-guardian"
  tar -C "$DIST_DIR" -czf "$DIST_DIR/$archive_name.tar.gz" "$archive_name"
  rm -rf "$stage_dir"
}

BINARY_NAME=jvm-oom-guardian
build_one linux amd64
build_one linux arm64
build_one darwin amd64
build_one darwin arm64

(
  cd "$DIST_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > SHA256SUMS
  else
    shasum -a 256 ./*.tar.gz > SHA256SUMS
  fi
)

echo "release artifacts created in $DIST_DIR"
ls -lh "$DIST_DIR"
