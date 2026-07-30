# ail-routing-test — predictive auto-router framework
#
# One-command demo on the small default config:  make all
# Individual stages:  make gen extract train eval
#
# All stages are seeded and idempotent; the Backend disk cache makes reruns
# free, so an interrupted overnight run resumes cheaply.

GO      ?= go
BIN     := bin
DATA    ?= data
CACHE   ?= cache

.PHONY: all gen extract train eval test build clean fmt vet tidy demo

## build: compile all command binaries into ./bin
build:
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/gen      ./cmd/gen
	$(GO) build -o $(BIN)/extract  ./cmd/extract
	$(GO) build -o $(BIN)/train    ./cmd/train
	$(GO) build -o $(BIN)/eval     ./cmd/eval

## gen: generate synthetic CC session logs (Pillar 1a)
gen: build
	$(BIN)/gen

## extract: raw logs -> structured dataset + extractor quality report (Pillar 1b/1c)
extract: build
	$(BIN)/extract

## train: fit candidate routers on the structured dataset (Pillar 2)
train: build
	$(BIN)/train

## eval: run the evaluation harness over trained routers (Pillar 3)
eval: build
	$(BIN)/eval

## all: full end-to-end pipeline, then print where results landed
all: gen extract train eval
	@echo ""
	@echo "== done. see RESULTS.md and $(DATA)/ for outputs =="

## demo: alias for all
demo: all

## test: unit tests for the portable core
test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

## clean: remove build output and regenerable data (keeps cache)
clean:
	rm -rf $(BIN) $(DATA)

## distclean: also drop the backend cache (forces real model calls next run)
distclean: clean
	rm -rf $(CACHE)
