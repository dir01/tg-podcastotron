build:
	go build -o ./bin/bot ./cmd/bot

run: docker-compose-up
	go run ./cmd/bot

test:
	go test -v ./...

generate:
	go generate ./...

docker-compose-up:
	docker-compose up -d
