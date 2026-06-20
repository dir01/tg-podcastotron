help: # Show help for each of the Makefile recipes.
	@grep -E '^[a-zA-Z0-9 -]+:.*#'  Makefile | while read -r l; do printf "\033[1;32m$$(echo $$l | cut -f 1 -d':')\033[00m:$$(echo $$l | cut -f 2- -d'#')\n"; done
.PHONY: help

build:  # Build the binary in ./bin/bot
	CGO_ENABLED=1 go build -o ./bin/bot ./cmd/bot
.PHONY: build

run: # Run the bot
	go run ./cmd/bot
.PHONY: run

run_all: # Run required services (from docker-compose.yml) and the bot
	docker-compose-up
	run
.PHONY: run_all

test:  # Run tests
	CGO_ENABLED=1 go test -v ./...
.PHONY: test

lint:  # Run linter
	golangci-lint run -v --timeout 5m
.PHONY: lint

generate:  # Generate code
	go generate ./...
.PHONY: generate

REGISTRY := ghcr.io
IMAGE := $(REGISTRY)/dir01/tg-podcastotron

build-image:  # Build the Docker image locally for the current platform
	docker build -t tg-podcastotron:local .
.PHONY: build-image

BUILDX_BUILDER := tg-podcastotron-builder

push-image:  # Build and push multi-platform image to ghcr.io (mirrors GHA)
	docker buildx build \
		--push \
		--tag $(IMAGE):sha-$(shell git rev-parse --short HEAD) \
		.
.PHONY: push-image

docker-compose-up:  # Run required services (from docker-compose.yml)
	docker-compose up -d
.PHONY: docker-compose-up

SQL_MIGRATE_CONFIG ?= ./db/dbconfig.yml
SQL_MIGRATE_ENV ?= development

new-migration: # Create a new migration
	go tool sql-migrate new -config "${SQL_MIGRATE_CONFIG}" $(shell bash -c 'read -p "Enter migration name: " name; echo $$name')
.PHONY: new-migration

migrate: # Migrate the database to the latest version
	sql-migrate up -config "${SQL_MIGRATE_CONFIG}" -env "${SQL_MIGRATE_ENV}"
.PHONY: migrate

migrate-down: # Rollback the database one version down
	sql-migrate down -config "${SQL_MIGRATE_CONFIG}" -env "${SQL_MIGRATE_ENV}"
.PHONY: migrate-down
