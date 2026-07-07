.PHONY: test build run-gateway run-media-api run-writer run-image-worker run-post-session-job run-pdf-renderer run-gmail-sender migrate-up migrate-down docs-schema

test:
	go test ./...

build:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/media-api ./cmd/media-api
	go build -o bin/writer ./cmd/writer
	go build -o bin/image-analysis-worker ./cmd/image-analysis-worker
	go build -o bin/post-session-job ./cmd/post-session-job
	go build -o bin/pdf-renderer ./cmd/pdf-renderer
	go build -o bin/gmail-sender ./cmd/gmail-sender

run-gateway:
	go run ./cmd/gateway

run-media-api:
	go run ./cmd/media-api

run-writer:
	go run ./cmd/writer

run-image-worker:
	go run ./cmd/image-analysis-worker

run-post-session-job:
	go run ./cmd/post-session-job

run-pdf-renderer:
	go run ./cmd/pdf-renderer

run-gmail-sender:
	go run ./cmd/gmail-sender

migrate-up:
	cd $(CURDIR)/.. && docker compose run --rm migrate up

migrate-down:
	cd $(CURDIR)/.. && docker compose run --rm migrate down 1

docs-schema:
	cd $(CURDIR)/.. && docker compose run --rm tbls
