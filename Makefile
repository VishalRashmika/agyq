.PHONY: build test lint fmt check clean

GO ?= go
VERSION ?= 0.0.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X agyq/internal/presentation.Version=$(VERSION) -X agyq/internal/presentation.Commit=$(COMMIT)

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o agyq ./cmd/agyq

test:
	$(GO) test ./...

fmt:
	gofmt -w cmd internal tools

# lint = gofmt check + go vet + architecture guardrail (fails the build on layer violations)
lint:
	@out="$$(gofmt -l cmd internal tools)"; if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) run ./tools/archcheck

check: build lint test

clean:
	rm -f agyq
