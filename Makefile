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

.PHONY: all gen extract train eval test build clean fmt vet tidy demo serve \
	agentic agentic-smoke agentic-tasks agentic-image agentic-proxy \
	agentic-proxy-stop agentic-gold agentic-eval

DATA_AGENTIC ?= data_agentic

## build: compile all command binaries into ./bin
build:
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/gen      ./cmd/gen
	$(GO) build -o $(BIN)/extract  ./cmd/extract
	$(GO) build -o $(BIN)/train    ./cmd/train
	$(GO) build -o $(BIN)/eval     ./cmd/eval
	$(GO) build -o $(BIN)/serve    ./cmd/serve
	$(GO) build -o $(BIN)/agentic  ./cmd/agentic

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

## serve: launch the web console (traces, data, fit, route) on :8080
serve: build
	$(BIN)/serve

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

# ===========================================================================
# Agentic, execution-grounded evaluation track (non-portable orchestration
# under agentic/, Python + Docker; schema/scoring/integration stays Go).
# See agentic/README.md and DECISIONS D12-D15.
# ===========================================================================

## agentic-tasks: (re)materialize + validate the executable task set
agentic-tasks:
	python3 agentic/runner/build_tasks.py
	python3 agentic/runner/validate_tasks.py

## agentic-image: build the hermetic pytest Docker image (once)
agentic-image:
	docker build -q -t agentic-runner:py311 agentic/exec/ >/dev/null && echo "image agentic-runner:py311 ready"

## agentic-proxy: start the Anthropic->Ollama proxy for the local arm
agentic-proxy:
	bash agentic/proxy/proxyctl.sh start

## agentic-proxy-stop: stop the proxy
agentic-proxy-stop:
	bash agentic/proxy/proxyctl.sh stop

## agentic-smoke: 1-task, BOTH arms, fidelity smoke (proves each arm can act)
agentic-smoke: build agentic-image agentic-proxy
	python3 agentic/runner/run_agentic.py --smoke

## agentic-gold: assemble the executed dual-arm gold set from runner results
agentic-gold: build
	$(BIN)/agentic -data-dir $(DATA_AGENTIC)

## agentic-eval: run the EXISTING eval harness on the agentic gold set
agentic-eval: build
	AIL_DATA_DIR=$(DATA_AGENTIC) $(BIN)/eval

## agentic: full pipeline — training data, both arms (resumable/cached),
## assemble executed gold, run the existing harness, write RESULTS_AGENTIC.md.
## The local arm is slow (open-weight, local GPU); reruns are free via the
## per-(task,arm) result cache.
agentic: build agentic-image
	@# 1) ensure synthetic training data exists (routers train on implicit labels)
	@test -f $(DATA)/pointwise.jsonl || $(MAKE) gen extract
	@# 2) start proxy + run both arms (cached; interrupted runs resume)
	bash agentic/proxy/proxyctl.sh start
	python3 agentic/runner/run_agentic.py --arms frontier,local
	@# 3) assemble executed gold + copy training data, run the existing harness
	$(BIN)/agentic -data-dir $(DATA_AGENTIC) -train-src $(DATA)
	AIL_DATA_DIR=$(DATA_AGENTIC) $(BIN)/eval
	@# 4) write RESULTS_AGENTIC.md
	python3 agentic/runner/report.py
	@echo ""
	@echo "== agentic done. see RESULTS_AGENTIC.md and $(DATA_AGENTIC)/ =="
