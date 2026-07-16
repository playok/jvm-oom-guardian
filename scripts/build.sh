#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT_DIR=${OUTPUT_DIR:-"$ROOT_DIR/bin"}
BINARY_NAME=${BINARY_NAME:-jvm-oom-guardian}
GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}
CGO_ENABLED=${CGO_ENABLED:-0}
GOCACHE=${GOCACHE:-"$ROOT_DIR/.cache/go-build"}
export GOCACHE

mkdir -p "$OUTPUT_DIR"
OUTPUT_PATH="$OUTPUT_DIR/$BINARY_NAME"

echo "building $BINARY_NAME for $GOOS/$GOARCH"
(
  cd "$ROOT_DIR"
  CGO_ENABLED="$CGO_ENABLED" GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags="-s -w" -o "$OUTPUT_PATH" .
)
chmod 0755 "$OUTPUT_PATH"
echo "created $OUTPUT_PATH"
