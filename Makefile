# Root Makefile — delegates to platform/web/engines

.PHONY: all build test lint clean dev

all: build

build: build-platform build-web

build-platform:
	cd platform && $(MAKE) build

build-web:
	cd web && npm ci && npm run build

test: test-platform test-web

test-platform:
	cd platform && $(MAKE) test

test-web:
	cd web && npx tsc --noEmit

lint: lint-platform

lint-platform:
	cd platform && $(MAKE) lint

dev-server:
	cd platform && go run ./cmd/server

dev-worker:
	cd platform && go run ./cmd/worker

dev-web:
	cd web && npm run dev

clean:
	cd platform && $(MAKE) clean
	rm -rf web/dist
