BINARY_NAME=factory
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

.PHONY: build test lint clean install up

build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/factory

install: build
	cp bin/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME) 2>/dev/null || cp bin/$(BINARY_NAME) ~/go/bin/$(BINARY_NAME)

test:
	go test ./... -v

test-short:
	go test ./... -short

lint:
	go vet ./...

clean:
	rm -rf bin/

up:
	./up

dev: build
	./bin/$(BINARY_NAME)
