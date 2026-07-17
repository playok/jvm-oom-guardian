.PHONY: build test release release-snapshot

build:
	./scripts/build.sh

test:
	./scripts/test.sh

release:
	goreleaser release --clean

release-snapshot:
	goreleaser release --snapshot --clean
