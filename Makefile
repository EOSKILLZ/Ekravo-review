BINARY := ekravo-review
VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG := github.com/ekravo/Ekravo-review/v2
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(BUILD_DATE)

.PHONY: build test lint run check migrate docker

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ekravo-review

test:
	go test -race ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

run:
	go run ./cmd/ekravo-review

check:
	go run ./cmd/ekravo-review -check

migrate:
	go run ./cmd/ekravo-review -migrate

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) -t ekravo-review:$(VERSION) .
