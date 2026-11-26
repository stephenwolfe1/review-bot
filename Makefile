VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: check build test image dry-run

check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run gofmt -w ." && exit 1)
	go vet ./...
	go test -race ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/review-bot ./cmd/review-bot

# Local image for the current platform. Releases are built and pushed by
# .github/workflows/release.yml.
image:
	docker build --build-arg VERSION=$(VERSION) -t review-bot:$(VERSION) .

# Review a real PR without posting anything. Needs the gh CLI (for a token)
# and a reachable model server:
#   LLM_BASE_URL=http://my-ollama:11434 make dry-run REPO=owner/repo PR=12
dry-run: build
	@test -n "$(REPO)" -a -n "$(PR)" || (echo "usage: make dry-run REPO=owner/repo PR=<number>" && exit 1)
	GITHUB_TOKEN=$$(gh auth token) bin/review-bot -repo $(REPO) -pr $(PR) -dry-run -force
