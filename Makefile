.PHONY: api-build api-run api-test

api-build:
	cd apps/api && go build -o bin/server ./cmd/server

api-run:
	cd apps/api && go run ./cmd/server

api-test:
	cd apps/api && go test ./...
