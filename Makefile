.PHONY: build frontend test dev

frontend:
	npm run build

build: frontend
	go build -o bin/differ ./cmd/differ

test: frontend
	go test ./...
	npm test

dev:
	go run ./cmd/differ
