.PHONY: run test lint build generate db-up db-down

run:
	go run ./cmd/bot

test:
	go test ./...

lint:
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/jobhawk ./cmd/bot

generate:
	sqlc generate

db-up:
	docker compose up -d postgres

db-down:
	docker compose down
