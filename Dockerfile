FROM golang:1.25-alpine AS app

RUN apk add --no-cache make gcc musl-dev

ADD go.mod go.sum ./
ENV GOPATH=""
ENV PATH="/root/go/bin:${PATH}"
RUN go mod download

ADD . .

ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN make build

CMD ["bin/bot"]
