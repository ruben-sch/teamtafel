# Teamtafel – Entwickler-Kurzbefehle
TEST_DATABASE_URL ?= postgres://teamtafel:teamtafel@localhost:5432/teamtafel_test?sslmode=disable

.PHONY: up down logs test test-unit lint build

up: ## Lokalen Stack starten (App, Postgres, Mailpit)
	docker compose up -d --build

down: ## Lokalen Stack stoppen
	docker compose down

logs: ## App-Logs verfolgen
	docker compose logs -f app

test: ## Alle Tests inkl. Integration gegen die lokale Datenbank
	docker compose up -d --wait db
	docker compose exec -T db psql -U teamtafel -d teamtafel -tc "SELECT 1 FROM pg_database WHERE datname='teamtafel_test'" | grep -q 1 \
		|| docker compose exec -T db createdb -U teamtafel teamtafel_test
	docker compose up -d --wait mailpit
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_SMTP_ADDR=localhost:1025 TEST_MAILPIT_URL=http://localhost:8025 go test -race ./...

test-unit: ## Nur Tests ohne Datenbank
	go test -race ./...

lint: ## go vet und golangci-lint
	go vet ./...
	golangci-lint run

build: ## Binary nach bin/ bauen
	CGO_ENABLED=0 go build -o bin/teamtafel ./cmd/teamtafel
