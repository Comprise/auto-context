VERSION ?= dev
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: test dist

test:
	go test ./...

dist:
	@for p in $(PLATFORMS); do \
		GOOS=$${p%/*}; GOARCH=$${p#*/}; EXT=; \
		if [ "$$GOOS" = windows ]; then EXT=.exe; fi; \
		echo "building $$GOOS/$$GOARCH"; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o libexec/auto-context-$$GOOS-$$GOARCH$$EXT ./cmd/auto-context || exit 1; \
	done
