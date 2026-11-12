.PHONY: up down logs test test-integration lint

up:            ## Build and start the local stack
	docker compose up -d --build

down:          ## Stop the local stack
	docker compose down

logs:
	docker compose logs -f panel redirect

test:          ## Unit tests (integration tests skip without a database)
	go test ./...

TEST_DB_URL = postgres://qr:qr@localhost:5433/qr_test?sslmode=disable

test-integration: ## Unit + integration tests against the compose database
	docker compose up -d --wait db
	docker compose exec -T db createdb -U qr qr_test 2>/dev/null || true
	TEST_DATABASE_URL='$(TEST_DB_URL)' go test -p 1 -count=1 ./...

lint:
	gofmt -l . | (! grep .)
	go vet ./...
