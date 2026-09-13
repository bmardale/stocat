# AGENTS.md

Guidance for AI agents and contributors working in this repository.

## Project

Go service. Database access goes through `sqlc`-generated code. Schema changes go through `goose` migrations. Code quality is enforced by `golangci-lint`.

## Commands

Tools run via `go tool` (declared in `go.mod`). Migration and admin targets require `DATABASE_URL`. The server also requires `APP_KEY`.

```sh
make build                      # go build ./...
make test                       # go test -race ./...
make lint                       # go tool golangci-lint run ./...
make generate                   # sqlc, OpenAPI document, and web API client
make generate-client            # OpenAPI document and web API client
make migrate-up                 # goose up
make migrate-down               # goose down
make migrate-status             # goose status
make migrate-create name=<name> # new SQL migration
make key-generate               # print a new APP_KEY
make admin-grant email=<email>  # make a user an administrator
make admin-revoke email=<email> # remove the administrator role
```

Run `make lint` and `make test` before you report a task as done. Do not disable a linter to make it pass. Fix the code.

## Layout

```
cmd/            entry points
internal/       application code (not importable from outside)
db/migrations/  goose SQL migrations
db/queries/     sqlc query files (*.sql)
db/sqlc/        generated code — never edit by hand
sqlc.yaml       sqlc config
.golangci.yml   lint config
```

## Database

- Every schema change is a new goose migration: `make migrate-create name=<name>`. Never edit an applied migration.
- Each migration must have a working `-- +goose Down` section.
- Write queries in `db/queries/*.sql`, then run `make generate`. Commit the generated code.
- Do not write raw SQL strings in Go. If a query does not exist, add it to `db/queries/`.
- Use `context.Context` for all database calls.

## Go Conventions

- Follow standard Go style. Run `gofmt` and `goimports`; `golangci-lint` will catch the rest.
- Return errors; do not panic in library code. Wrap with context: `fmt.Errorf("load user %d: %w", id, err)`.
- Keep functions small and packages focused. Prefer the standard library over a dependency.
- Tests live next to the code they test. Use table-driven tests. Do not mock the database; use a test database with migrations applied.
- Do not add a dependency without a clear reason. Run `go mod tidy` after changes.

## Comments and Documentation

Write all comments, doc strings, commit messages, and documentation in **ASD-STE100 Simplified Technical English**.

* Use short sentences. Use a maximum of 20 words per sentence.
* Use the active voice.
* Use the imperative mood for instructions.
* Use one word for one meaning.
* Avoid vague words such as "some", "several", "appropriate", and "as needed".

**Prefer no comment over an obvious comment.**

Add a comment only when it explains information that the code cannot express clearly.

Do not add comments that:

* Restate an identifier name.
* Restate a function signature.
* Restate a field name or type.
* Describe an obvious implementation.
* Describe control flow that is clear from the code.
* Mark sections of code.
* Explain standard Go syntax or standard library behavior.
* Repeat information already present in a nearby identifier.
* Add prose only because an identifier is exported.

Do not add comments to exported struct fields unless the field has a non-obvious contract, constraint, unit, format, or behavior.

Do not add doc comments to exported identifiers unless:

* The configured linter requires the comment.
* The identifier is part of a public package API and the comment adds useful information.

When a linter requires a doc comment, write the shortest useful comment that satisfies the linter.

Before you add a comment, ask: "Does this comment tell the reader something they cannot learn directly from the code?" If the answer is no, do not add it.

## Git

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>
```

- Types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`.
- Scope is optional: the package or area touched, e.g. `feat(auth): add session refresh`.
- Subject: imperative, lowercase, no trailing period, under 72 characters.
- Body: explain why, not what. Omit it when the subject is enough.
- Breaking changes: add `!` after the type (`feat(api)!: ...`) and a `BREAKING CHANGE:` footer.
- Reference issues in the footer: `Closes #42`.
- One logical change per commit. Never commit secrets, `.env` files, or local database dumps.
- Do not force-push shared branches.
