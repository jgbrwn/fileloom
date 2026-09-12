.PHONY: build editor-build clean test run fmt

build:
	go build -o fileloom ./cmd/fileloom

editor-build:
	npm ci --prefix web/editor
	npm run build --prefix web/editor

run:
	go run ./cmd/fileloom

fmt:
	gofmt -w ./cmd ./srv

test:
	go test ./...
