FROM golang:1.26.10-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/ekravo/Ekravo-review/v2/internal/version.Version=${VERSION} -X github.com/ekravo/Ekravo-review/v2/internal/version.Commit=${COMMIT} -X github.com/ekravo/Ekravo-review/v2/internal/version.Date=${BUILD_DATE}" \
    -o /out/ekravo-review ./cmd/ekravo-review

# git is required for monorepo partial clones and sparse checkout. The binary
# itself is static; the shell is only present for git's helpers.
FROM alpine:3.22

RUN apk add --no-cache git ca-certificates \
    && addgroup -S ekravo \
    && adduser -S -G ekravo -h /data ekravo \
    && mkdir -p /data/.ekravo-context \
    && chown -R ekravo:ekravo /data

COPY --from=build /out/ekravo-review /usr/local/bin/ekravo-review

WORKDIR /data
USER ekravo
EXPOSE 8090

ENV DB_PATH=/data/ekravo-review.db \
    CONTEXT_CACHE_DIR=/data/.ekravo-context
ENTRYPOINT ["ekravo-review"]
