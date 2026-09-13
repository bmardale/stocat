.PHONY: build test test-unit lint generate generate-db generate-openapi generate-client migrate-up migrate-down migrate-reset migrate-status migrate-create

GOOSE := GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$DATABASE_URL" go tool goose -dir db/migrations

build:
	go build ./...

test:
	go test -race ./...

test-unit:
	go test -race -short ./...

lint:
	go tool golangci-lint run ./...

generate: generate-db generate-client

generate-db:
	go tool sqlc generate

generate-openapi:
	go run ./cmd/openapi

generate-client: generate-openapi
	cd web && vp run generate:api

require-db:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is required" >&2; exit 1)

migrate-up: require-db
	$(GOOSE) up

migrate-down: require-db
	$(GOOSE) down

migrate-reset: require-db
	$(GOOSE) reset

migrate-status: require-db
	$(GOOSE) status

migrate-create: require-db
	@test -n "$$name" || (echo "usage: make migrate-create name=add_users" >&2; exit 1)
	$(GOOSE) -s create $$name sql
