# SIIASQL MCP — read-only PostgreSQL MCP server for SIIA.
# Fast checks run everywhere; integration and scheduled tiers need containers.

GO      ?= go
BIN     := bin/siiasql
PG_VERS := 15 16 17 18
# Per-target fuzz budgets: FUZZ_TIME for smoke runs, FUZZ_TIME_LONG for scheduled.
# Each entry is "<pkg>:<FuzzTarget>". Go's -fuzz flag requires a single package.
FUZZ_TIME       ?= 15s
FUZZ_TIME_LONG  ?= 10m

.PHONY: all
all: fast

## ---------- Fast checks (every change) ----------
.PHONY: fmt-check
fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi; \
	echo "gofmt clean"

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: build
build:
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/siiasql

.PHONY: fast
fast: fmt-check vet test test-race build

## ---------- Merge-bound checks ----------
.PHONY: e2e
e2e:
	$(GO) test -tags=e2e -count=1 ./internal/e2e/

# Integration runs against an ephemeral PostgreSQL container (Docker or Podman).
# The script seeds scripts/fixtures/it.sql and sets SIIASQL_IT_* automatically.
.PHONY: it
it: ## default integration tier: PG 15 + PG 18 (merge-bound matrix)
	@./scripts/it-pg.sh 15
	@./scripts/it-pg.sh 18

.PHONY: merge-bound
merge-bound: it e2e build

## ---------- Scheduled / release ----------
.PHONY: it-matrix
it-matrix: ## full 15-18 PostgreSQL matrix
	@for v in $(PG_VERS); do ./scripts/it-pg.sh $$v; done

.PHONY: fuzz
fuzz: ## bounded fuzz smoke over every target (seeds + FUZZ_TIME each)
	@for pair in ./internal/config/:FuzzParse \
	             ./internal/postgres/guard/:FuzzCheck \
	             ./internal/cache/:FuzzBounds \
	             ./internal/postgres/:FuzzEncodeValue \
	             ./internal/postgres/:FuzzEncodeParam \
	             ./internal/postgres/:FuzzHexToBytes; do \
		pkg=$${pair%%:*}; tgt=$${pair##*:}; \
		echo "== fuzzing $$tgt in $$pkg ($(FUZZ_TIME)) =="; \
		$(GO) test $$pkg -run=NONE -fuzz=$$tgt -fuzztime=$(FUZZ_TIME) || exit 1; \
	done

.PHONY: fuzz-long
fuzz-long: ## extended fuzz per target (FUZZ_TIME_LONG each) — scheduled/release
	@for pair in ./internal/config/:FuzzParse \
	             ./internal/postgres/guard/:FuzzCheck \
	             ./internal/cache/:FuzzBounds \
	             ./internal/postgres/:FuzzEncodeValue \
	             ./internal/postgres/:FuzzEncodeParam \
	             ./internal/postgres/:FuzzHexToBytes; do \
		pkg=$${pair%%:*}; tgt=$${pair##*:}; \
		echo "== fuzzing $$tgt in $$pkg ($(FUZZ_TIME_LONG)) =="; \
		$(GO) test $$pkg -run=NONE -fuzz=$$tgt -fuzztime=$(FUZZ_TIME_LONG) || exit 1; \
	done

.PHONY: cross-build
cross-build: ## native build gate (cgo forbids single-host cross-compile)
	@echo "Native build for $$(go env GOOS)/$$(go env GOARCH):"
	CGO_ENABLED=1 $(GO) build -o /tmp/siiasql-native ./cmd/siiasql
	@echo "OK"

## ---------- Misc ----------
.PHONY: clean
clean:
	rm -rf bin

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/## /  /'
