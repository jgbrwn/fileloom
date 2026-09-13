.PHONY: build editor-build editor-test clean test run fmt

build:
	go build -o fileloom ./cmd/fileloom

editor-build:
	npm ci --prefix web/editor
	npm run build --prefix web/editor

editor-test:
	npm run test:e2e --prefix web/editor

run:
	go run ./cmd/fileloom

fmt:
	gofmt -w ./cmd ./srv

test:
	go test ./...
