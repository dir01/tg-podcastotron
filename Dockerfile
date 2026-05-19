FROM golang:1.25-alpine AS builder

RUN apk add --no-cache make gcc musl-dev

WORKDIR /build
ADD go.mod go.sum ./
ENV GOPATH=""
RUN go mod download

ADD . .

ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN make build

FROM alpine:3.21 AS app

COPY --from=builder /build/bin/bot /bin/bot

CMD ["/bin/bot"]
