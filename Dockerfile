# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder

RUN apk add --no-cache make gcc musl-dev

WORKDIR /build

ENV GOPATH="" \
    GOCACHE=/root/.cache/go-build \
    GOMODCACHE=/root/go/pkg/mod \
    CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

ADD go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod \
    go mod download

ADD . .

# Cache mounts keep the compiled Go build cache and module cache warm across
# builds (and the bot and the tools share one layer). The tools go to
# /build/tools as <name>-<version> with a <name> link, like in ./bin locally.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg/mod \
    make build && \
    make sql-migrate LOCALBIN=/build/tools

FROM alpine:3.24 AS app

RUN apk add --no-cache make

WORKDIR /app
COPY --from=builder /build/bin/bot /bin/bot
# `make migrate` (the init container) finds sql-migrate in ./bin and does not install it.
COPY --from=builder /build/tools/ ./bin/
COPY --from=builder /build/Makefile ./Makefile
COPY --from=builder /build/db/migrations ./db/migrations

# The Mini App HTTP server (HTTP_ADDR)
EXPOSE 8080

CMD ["/bin/bot"]
