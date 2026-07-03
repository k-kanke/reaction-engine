.PHONY: test build run-gateway run-media-api run-writer run-image-worker run-post-session-job

test:
	go test ./...

build:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/media-api ./cmd/media-api
	go build -o bin/writer ./cmd/writer
	go build -o bin/image-analysis-worker ./cmd/image-analysis-worker
	go build -o bin/post-session-job ./cmd/post-session-job

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
