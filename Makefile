.PHONY: build editor-build editor-test editor-test-csp editor-test-all clean test run fmt

build:
	go build -o fileloom ./cmd/fileloom

editor-build:
	npm ci --prefix web/editor
	npm run build --prefix web/editor

editor-test:
	npm run test:e2e --prefix web/editor

editor-test-csp:
	FILELOOM_E2E_CSP=default npm run test:e2e --prefix web/editor

editor-test-all: editor-test editor-test-csp

run:
	go run ./cmd/fileloom

fmt:
	gofmt -w ./cmd ./srv

test:
	go test ./...
