.PHONY: build test lint coverage check

COVERAGE_THRESHOLD ?= 85.0

build:
	go build -o bin/wacrawl ./cmd/wacrawl

test:
	go test ./...

lint:
	golangci-lint run ./...

coverage:
	./scripts/coverage.sh $(COVERAGE_THRESHOLD)

check: lint coverage build
