BINARY      := ytb-comment-downloader
PKG         := github.com/luca/ytb-comment-downloader-go
DIST        := dist
DOCKER_TAG  ?= ytb-comment-downloader:latest

GO          ?= go
BUILDFLAGS  := -trimpath -ldflags="-s -w"

.PHONY: all build test vet fmt run serve clean dist \
        build-linux-amd64 build-linux-arm64 build-darwin-arm64 build-darwin-amd64 \
        docker docker-push tidy

all: vet test build

build:
	CGO_ENABLED=0 $(GO) build $(BUILDFLAGS) -o $(BINARY) .

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

run: build
	./$(BINARY) $(ARGS)

serve: build
	./$(BINARY) serve --addr=:8080

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)

# ---- Cross-compilation -----------------------------------------------------

dist: build-linux-amd64 build-linux-arm64 build-darwin-arm64 build-darwin-amd64

$(DIST):
	mkdir -p $(DIST)

build-linux-amd64: | $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build $(BUILDFLAGS) -o $(DIST)/$(BINARY)-linux-amd64 .

build-linux-arm64: | $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		$(GO) build $(BUILDFLAGS) -o $(DIST)/$(BINARY)-linux-arm64 .

build-darwin-arm64: | $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
		$(GO) build $(BUILDFLAGS) -o $(DIST)/$(BINARY)-darwin-arm64 .

build-darwin-amd64: | $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 \
		$(GO) build $(BUILDFLAGS) -o $(DIST)/$(BINARY)-darwin-amd64 .

# ---- Docker ---------------------------------------------------------------

docker:
	docker build -t $(DOCKER_TAG) .

docker-push: docker
	docker push $(DOCKER_TAG)
