FROM golang:1.21-alpine as app

RUN apk add --no-cache make gcc musl-dev

ADD go.mod go.sum ./
ENV GOPATH ""
RUN go mod download

ADD . .
RUN make build

CMD bin/bot
