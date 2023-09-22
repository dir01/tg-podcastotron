FROM golang:1.21-alpine as app

RUN apk add --no-cache make gcc musl-dev

ADD go.mod go.sum ./
ENV GOPATH ""
ENV PATH="/root/go/bin:${PATH}"
RUN go mod download

ADD . .

RUN make install-dev  # so that the same image could be used to run migrations
RUN make build


CMD bin/bot
