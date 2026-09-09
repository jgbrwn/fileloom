.PHONY: build clean test run

build:
	go build -o fileloom ./cmd/fileloom

run:
	go run ./cmd/fileloom

clean:
	rm -f fileloom

test:
	go test ./...
