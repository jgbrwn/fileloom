.PHONY: build clean test run fmt

build:
	go build -o fileloom ./cmd/fileloom

run:
	go run ./cmd/fileloom

fmt:
	gofmt -w ./cmd ./srv

test:
	go test ./...
