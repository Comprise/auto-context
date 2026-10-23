VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
# The one place with the release build flags: release.yml calls `make dist`.
BUILD = CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
TOOL = go tool -modfile=tools/go.mod

.PHONY: build test lint verify dist

# build: the binary for this host into libexec/, where bin/auto-context finds it.
build:
	@GOOS=$$(go env GOOS); GOARCH=$$(go env GOARCH); EXT=; \
	if [ "$$GOOS" = windows ]; then EXT=.exe; fi; \
	$(BUILD) -o libexec/auto-context-$$GOOS-$$GOARCH$$EXT ./cmd/auto-context

# test: with the race detector, which needs cgo and a C compiler; without one,
# `make test RACE=`.
RACE ?= -race
test:
	go vet ./...
	go test $(RACE) ./...

# lint: what the CI lint job checks. shellcheck is skipped when not installed
# (CI installs a pinned one); govulncheck needs the network.
lint:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt -l:"; echo "$$out"; exit 1; fi
	go vet ./...
	$(TOOL) staticcheck ./...
	$(TOOL) govulncheck ./...
	$(TOOL) actionlint
	@if command -v shellcheck >/dev/null; then \
		shellcheck -s sh -S warning bin/auto-context .github/scripts/*.sh; \
	else echo "shellcheck not installed, skipped"; fi
	sh -n bin/auto-context

verify:
	go mod verify
	cd tools && go mod verify
	sh .github/scripts/check-manifests.sh .
	sh -n bin/auto-context

# dist: all platforms into a clean libexec/ (no leftovers of other targets).
dist:
	rm -rf libexec
	@for p in $(PLATFORMS); do \
		GOOS=$${p%/*}; GOARCH=$${p#*/}; EXT=; \
		if [ "$$GOOS" = windows ]; then EXT=.exe; fi; \
		echo "building $$GOOS/$$GOARCH $(VERSION)"; \
		GOOS=$$GOOS GOARCH=$$GOARCH $(BUILD) -o libexec/auto-context-$$GOOS-$$GOARCH$$EXT ./cmd/auto-context || exit 1; \
	done
