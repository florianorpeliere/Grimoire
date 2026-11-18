# Common commands. Database targets read DATABASE_URL from the environment.

.PHONY: build vet test migrate srd-dry-run

build:
	go build -o bin/ ./...

vet:
	go vet ./...

test:
	go test -count=1 ./...

migrate:
	go run ./cmd/ingest migrate

srd-dry-run:
	go run ./cmd/ingest srd -dry-run
