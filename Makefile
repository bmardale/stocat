.PHONY: build test test-unit lint generate generate-db generate-openapi generate-client key-generate admin-grant admin-revoke migrate-up migrate-down migrate-reset migrate-status migrate-create

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

key-generate:
	@echo "APP_KEY=$$(go run ./cmd/keygen)"

admin-grant: require-db
	@test -n "$$email" || (echo "usage: make admin-grant email=user@example.com" >&2; exit 1)
	go run ./cmd/admin grant "$$email"

admin-revoke: require-db
	@test -n "$$email" || (echo "usage: make admin-revoke email=user@example.com" >&2; exit 1)
	go run ./cmd/admin revoke "$$email"

require-db:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is required" >&2; exit 1)

migrate-up: require-db
	$(GOOSE) up
	go tool river migrate-up --database-url "$$DATABASE_URL"

migrate-down: require-db
	$(GOOSE) down

migrate-reset: require-db
	go tool river migrate-down --database-url "$$DATABASE_URL" --target-version 0
	$(GOOSE) reset
	go tool river migrate-up --database-url "$$DATABASE_URL"

migrate-status: require-db
	$(GOOSE) status
	go tool river validate --database-url "$$DATABASE_URL"

migrate-create: require-db
	@test -n "$$name" || (echo "usage: make migrate-create name=add_users" >&2; exit 1)
	$(GOOSE) -s create $$name sql
