FROM golang:1.19-alpine as app
RUN apk add --no-cache make

ADD go.mod go.sum ./
ENV GOPATH ""
RUN go mod download

ADD . .
RUN make build

CMD bin/bot
