.PHONY: build test lint clean run-server run-worker

GOOS    ?= linux
GOARCH  ?= amd64
CGO_ENABLED ?= 0

build-all: test lint build

build:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags="-s -w" -o bin/yuqing-server ./cmd/server
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags="-s -w" -o bin/yuqing-worker ./cmd/worker
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags="-s -w" -o bin/yuqing-cli ./cmd/cli

test:
	go test ./... -count=1 -race

test-cover:
	go test ./... -count=1 -race -coverprofile=coverage.out
	go tool cover -func=coverage.out

test-verbose:
	go test ./... -v -count=1 -race

lint:
	go vet ./...

run-server:
	go run ./cmd/server

run-worker:
	go run ./cmd/worker

clean:
	rm -rf bin/* coverage.out
