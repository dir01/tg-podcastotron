FROM golang:1.26-alpine AS builder

RUN apk add --no-cache make gcc musl-dev

WORKDIR /build
ADD go.mod go.sum ./
ENV GOPATH=""
RUN go mod download

ADD . .

ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN make build
RUN go build -o /build/bin/sql-migrate github.com/rubenv/sql-migrate/sql-migrate

FROM alpine:3.24 AS app

RUN apk add --no-cache make

WORKDIR /app
COPY --from=builder /build/bin/bot /bin/bot
COPY --from=builder /build/bin/sql-migrate /usr/local/bin/sql-migrate
COPY --from=builder /build/Makefile ./Makefile
COPY --from=builder /build/db/migrations ./db/migrations

CMD ["/bin/bot"]
