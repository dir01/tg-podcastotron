help: # Show help for each of the Makefile recipes.
	@grep -E '^[a-zA-Z0-9 -]+:.*#'  Makefile | while read -r l; do printf "\033[1;32m$$(echo $$l | cut -f 1 -d':')\033[00m:$$(echo $$l | cut -f 2- -d'#')\n"; done
.PHONY: help

build:  # Build the binary in ./bin/bot
	CGO_ENABLED=1 go build -o ./bin/bot ./cmd/bot

run: # Run the bot
	go run ./cmd/bot
.PHONY: run

run_all: # Run required services (from docker-compose.yml) and the bot
	docker-compose-up
	run
.PHONY: run_all

test:  # Run tests
	go test -v ./...
.PHONY: test

lint:  # Run linter
	docker run -t --rm -v $$(pwd):/app -w /app golangci/golangci-lint:v2.0.2 golangci-lint run -v --timeout 5m
.PHONY: lint

generate:  # Generate code
	go generate ./...

docker-compose-up:  # Run required services (from docker-compose.yml)
	docker-compose up -d
.PHONY: docker-compose-up

install-dev: # Install development dependencies
	go install github.com/rubenv/sql-migrate/...@latest
.PHONY: install-dev

SQL_MIGRATE_CONFIG ?= ./db/dbconfig.yml
SQL_MIGRATE_ENV ?= development

new-migration: # Create a new migration
	sql-migrate new -config "${SQL_MIGRATE_CONFIG}" $(shell bash -c 'read -p "Enter migration name: " name; echo $$name')

migrate: # Migrate the database to the latest version
	sql-migrate up -config "${SQL_MIGRATE_CONFIG}" -env "${SQL_MIGRATE_ENV}"
.PHONY: migrate

migrate-down: # Rollback the database one version down
	sql-migrate down -config "${SQL_MIGRATE_CONFIG}" -env "${SQL_MIGRATE_ENV}"
.PHONY: migrate-down
