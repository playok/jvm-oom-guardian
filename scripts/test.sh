#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GOCACHE=${GOCACHE:-"$ROOT_DIR/.cache/go-build"}
export GOCACHE

cd "$ROOT_DIR"

echo "checking formatting"
if [ -n "$(gofmt -l .)" ]; then
  echo "gofmt required for the files above" >&2
  exit 1
fi

echo "running unit tests with race detector"
go test -race ./...

echo "running vet"
go vet ./...

if [ "${E2E:-0}" = "1" ]; then
  if ! command -v java >/dev/null 2>&1 || ! command -v javac >/dev/null 2>&1; then
    echo "E2E=1 requires java and javac" >&2
    exit 1
  fi
  echo "E2E integration test is not bundled into go test; use the documented OOM fixture manually"
fi

echo "tests passed"
