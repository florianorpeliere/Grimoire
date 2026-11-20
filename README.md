# Grimoire

A game-master assistant for tabletop RPGs compatible with 5e. It answers rules questions with
citations from the SRD 5.2.1, and campaign questions from the GM's session notes.

Work in progress. Everything runs locally with native services: no Docker, no hosted service.

## Requirements

- Go 1.27
- PostgreSQL 17 with the [pgvector](https://github.com/pgvector/pgvector) extension

## Local setup (macOS, Homebrew)

The project default port for PostgreSQL is **5433**, so it does not clash with another
PostgreSQL instance on the default port 5432. If you have no other instance, you can keep 5432
and adapt `DATABASE_URL`.

1. Install PostgreSQL 17 and pgvector:

   ```sh
   brew install postgresql@17 pgvector
   ```

   The Homebrew binaries of `postgresql@17` are not on your `PATH` by default; they live in
   `$(brew --prefix postgresql@17)/bin`. Use that `psql` and `createdb` below, so you do not talk
   to another PostgreSQL installation by mistake.

2. Run the server on port 5433: set `port = 5433` in its `postgresql.conf` (in the data directory,
   `$(brew --prefix)/var/postgresql@17` by default), then start it:

   ```sh
   brew services start postgresql@17
   ```

3. Create the two databases, `grimoire` for development and `grimoire_test` for the tests:

   ```sh
   PG="$(brew --prefix postgresql@17)/bin"
   "$PG/createdb" -p 5433 grimoire
   "$PG/createdb" -p 5433 grimoire_test
   ```

   The first migration runs `CREATE EXTENSION IF NOT EXISTS vector`, so the role you connect
   with must be allowed to create extensions (the Homebrew default role is a superuser).

4. Configure the environment. `.env.example` lists the variables, with placeholders:

   ```sh
   cp .env.example .env    # replace USER, then:
   set -a; . ./.env; set +a
   ```

   The connection string is read from `DATABASE_URL` only; there is no default. It must state
   the host, the port and the database name explicitly, otherwise the commands refuse it: the
   PostgreSQL driver would fill the missing parts from `PG*` variables or from its defaults
   (port 5432) and could connect to another instance. In the examples below, replace `USER`
   with your PostgreSQL role.

   The tests never read `DATABASE_URL`: integration tests read `TEST_DATABASE_URL`, which
   `.env.example` leaves commented out, so loading `.env` does not turn them on. To enable
   them, uncomment it in `.env` or set it for one run (see [Build and test](#build-and-test)).
   It follows the same rules and must point at `grimoire_test`.

## Migrations

Migrations live in `internal/db/migrations` and are applied with [goose](https://github.com/pressly/goose),
embedded in the `ingest` command. They are never applied automatically: run them explicitly,
once per database.

```sh
DATABASE_URL="postgres://USER@localhost:5433/grimoire?sslmode=disable" make migrate
DATABASE_URL="postgres://USER@localhost:5433/grimoire_test?sslmode=disable" make migrate
```

Running it again when the database is up to date applies nothing and exits 0.

## SRD ingestion

Get the SRD Markdown with `scripts/srd-download.sh` (see [docs/srd-source.md](docs/srd-source.md)),
apply the migrations, then:

```sh
make srd-dry-run   # chunk report only, no database
make srd           # store the chunks in the database at DATABASE_URL
```

`make srd` upserts the chunks into the `srd_chunks` table by id and deletes the stored chunks
that are no longer in the file, so running it again on the same file changes nothing. It prints
how many chunks were inserted, updated, unchanged and deleted, and the number of rows stored.
The table has a full-text index over each chunk's title, short citation path and text, used by
the keyword search in `internal/search`.

## Build and test

```sh
make build   # binaries in bin/
make vet
make test
```

Integration tests need PostgreSQL. They read their connection string from `TEST_DATABASE_URL`
only, never from `DATABASE_URL`, which is for the application. They run only against the
`grimoire_test` database, and they reset its schema. They never start a service.

- `TEST_DATABASE_URL` unset: the integration tests are skipped with a message. This is the only
  case that skips, so `make test` with only a development `DATABASE_URL` loaded runs the unit
  tests and touches no database.
- `TEST_DATABASE_URL` set: anything that prevents the integration tests from running fails them,
  with a message naming the case: empty, incomplete or malformed URL, unreachable server,
  missing database, failed authentication, or a database other than `grimoire_test`.

```sh
TEST_DATABASE_URL="postgres://USER@localhost:5433/grimoire_test?sslmode=disable" make test
```

Use `go test -v ./...` to see which tests were skipped and why.

## Layout

```
cmd/ingest/        database migrations, SRD chunking dry run and storage
internal/db/       connection and embedded migrations
internal/srd/      SRD Markdown split into chunks (see docs/srd-chunking.md), and their storage
internal/search/   keyword (full-text) search over the SRD chunks
internal/dbtest/   helpers for integration tests
```
