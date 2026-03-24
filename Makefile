VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -X github.com/trngle-xyz/cli/internal/tui.Version=$(VERSION)
BINARY  = trngle
GOFLAGS = -trimpath

# Build targets
PLATFORMS = \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: build clean test release

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/trngle

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

# Build all platforms into dist/
release: clean
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		outname="$(BINARY)-$$os-$$arch$$ext"; \
		echo "Building $$outname..."; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o "dist/$$outname" ./cmd/trngle || exit 1; \
	done
	@echo "Done. Binaries in dist/"
	@ls -lh dist/
