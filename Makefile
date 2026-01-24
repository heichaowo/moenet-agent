.PHONY: build clean test run

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)

build:
	go build -ldflags="$(LDFLAGS)" -o moenet-agent ./cmd/moenet-agent

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o moenet-agent-linux-amd64 ./cmd/moenet-agent

clean:
	rm -f moenet-agent moenet-agent-linux-*

test:
	go test -v ./...

run: build
	./moenet-agent -c configs/config.example.json

fmt:
	go fmt ./...

lint:
	golangci-lint run

deps:
	go mod tidy
	go mod download
