.PHONY: api-build api-run api-test sidecar-build sidecar-run

api-build:
	cd apps/api && go build -o bin/server ./cmd/server

api-run:
	cd apps/api && go run ./cmd/server

api-test:
	cd apps/api && go test ./...

sidecar-build:
	cd services/gmtrade-sidecar && cargo build --release

sidecar-run:
	cd services/gmtrade-sidecar && cargo run
