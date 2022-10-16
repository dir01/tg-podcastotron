build:
	go build -o ./bin/bot ./cmd/bot

run:
	go run ./cmd/bot

test:
	go test -v ./...
