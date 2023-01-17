build:
	go build -o ./bin/bot ./cmd/bot

run-server:
	go run ./cmd/bot

run_all: docker-compose-up run-server

test:
	go test -v ./...

generate:
	go generate ./...

docker-compose-up:
	docker-compose up -d
