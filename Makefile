build:
	go build -o ./bin/bot ./cmd/bot

run:
	go run ./cmd/bot

run_all: docker-compose-up run-server

test:
	go test -v ./...

lint:
	docker run -t --rm -v $$(pwd):/app -w /app golangci/golangci-lint:v1.50.1 golangci-lint run -v

generate:
	go generate ./...

docker-compose-up:
	docker-compose up -d
