BINARY := ekravo-review
VERSION ?= 0.1.0
PKG := github.com/EOSKILLZ/ekravo-review/v2
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)

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
	docker build --build-arg VERSION=$(VERSION) -t ekravo-review:$(VERSION) .
