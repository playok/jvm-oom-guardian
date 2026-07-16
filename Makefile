.PHONY: build test release

build:
	./scripts/build.sh

test:
	./scripts/test.sh

release:
	./scripts/release.sh $(VERSION)
