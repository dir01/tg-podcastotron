# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder

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
# builds (and the two binaries share one layer).
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg/mod \
    make build && \
    go build -o /build/bin/sql-migrate github.com/rubenv/sql-migrate/sql-migrate

FROM alpine:3.24 AS app

RUN apk add --no-cache make

WORKDIR /app
COPY --from=builder /build/bin/bot /bin/bot
COPY --from=builder /build/bin/sql-migrate /usr/local/bin/sql-migrate
COPY --from=builder /build/Makefile ./Makefile
COPY --from=builder /build/db/migrations ./db/migrations

CMD ["/bin/bot"]
