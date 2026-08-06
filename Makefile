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
	agentic-proxy-stop agentic-gold agentic-materialize agentic-train agentic-eval \
	agentic-fit-eval agentic-swe agentic-generate agentic-split

DATA_AGENTIC ?= data_agentic

# Agentic model roster (log-first generation). Frontier = Opus via the logged-in
# subscription; local = gpt-oss:20b via the Anthropic->Ollama proxy (D16: 100%
# native tool fidelity). Exported so the Python runner + proxy inherit them.
FRONTIER_MODEL      ?= opus
PROXY_OLLAMA_MODEL  ?= gpt-oss:20b
SWE_N               ?= 20
export FRONTIER_MODEL PROXY_OLLAMA_MODEL

## build: compile all command binaries into ./bin
build:
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/gen      ./cmd/gen
	$(GO) build -o $(BIN)/extract  ./cmd/extract
	$(GO) build -o $(BIN)/train    ./cmd/train
	$(GO) build -o $(BIN)/eval     ./cmd/eval
	$(GO) build -o $(BIN)/serve    ./cmd/serve
	$(GO) build -o $(BIN)/agentic  ./cmd/agentic
	$(GO) build -o $(BIN)/materialize ./cmd/materialize

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

## agentic-grade: offline executed-oracle branch — run hidden tests on each
## session's produced patch -> labels/executed.jsonl (docker_pytest needs the
## executor image; swebench needs the swebench venv; ungradeable sessions skipped).
agentic-grade:
	docker build -q -t agentic-runner:py311 agentic/exec/ >/dev/null
	python3 agentic/runner/grade_offline.py

## agentic-calibrate: score judge/heuristics vs executed truth + fuse weak labels
## (judge-primary) into canonical labels. No model call. Writes calibration/report.json
## + labels/resolved.jsonl.
agentic-calibrate:
	$(GO) run ./cmd/label -calibrate

## agentic-heuristics: mine implicit labels from sim-user reactions in each
## session's RawTurn log (deterministic, no model call). Rewrites labels/implicit.jsonl.
agentic-heuristics:
	$(GO) run ./cmd/label -heuristics

## agentic-smoke: 1-task, BOTH arms, fidelity smoke (proves each arm can act).
## Generation is grading-free, so it does NOT build the Docker executor image.
agentic-smoke: build agentic-proxy
	python3 agentic/runner/run_agentic.py --smoke

## SWEBENCH_PY: a python with swebench + datasets (used to build instance images)
SWEBENCH_PY ?= $(HOME)/development/spectro/ail-self-routing/.venv_swe/bin/python

## agentic-swe-images: build the official SWE-bench per-instance images so the
## agent runs INSIDE the real env (base -> env -> instance; x86_64, emulated).
agentic-swe-images:
	$(SWEBENCH_PY) agentic/runner/build_swe_images.py

## agentic-swe: materialize N SWE-bench Verified instances, build their images,
## then run BOTH arms IN-CONTAINER (log-first, NO grading). SWE_N controls count.
agentic-swe: build agentic-proxy
	python3 agentic/runner/materialize_swe.py --n $(SWE_N)
	$(MAKE) agentic-swe-images
	python3 agentic/runner/run_agentic.py --arms local,frontier
	python3 agentic/runner/split.py

## agentic-generate: run BOTH arms over ALL materialized tasks (log-first, no
## grading) then write the train/held-out split manifest. The generation entry.
agentic-generate: build agentic-proxy
	python3 agentic/runner/run_agentic.py --arms local,frontier
	python3 agentic/runner/split.py

## agentic-split: (re)write split_manifest.json over existing results (Phase 4)
agentic-split:
	python3 agentic/runner/split.py

## agentic-materialize: offline-engine bridge (O6) — turn the fused canonical
## labels (labels/resolved.jsonl) into pointwise/pairwise/gold for train + eval.
## SUPERSEDES agentic-gold: outcomes come from the engine's calibrated labels,
## gold is EXECUTED-only from the HOLDOUT split, oracle-ungraded sessions are
## quarantined. Run after agentic-grade + agentic-calibrate.
agentic-materialize: build
	$(BIN)/materialize -data-dir $(DATA_AGENTIC)

## agentic-gold: [DEPRECATED — use agentic-materialize] assemble the executed
## dual-arm gold set directly from runner `resolved` fields. Retained until the
## BuildGold guard lands on the data-plan branch; do not use on fresh log-first
## data (it reads a `resolved` field the runner no longer emits).
agentic-gold: build
	$(BIN)/agentic -data-dir $(DATA_AGENTIC)

## agentic-train: fit the routers on the materialized agentic pointwise/pairwise.
## Roster (gpt-oss:20b / opus) is auto-detected from the data (dataio.ResolveRoster).
agentic-train: build
	AIL_DATA_DIR=$(DATA_AGENTIC) $(BIN)/train

## agentic-eval: run the EXISTING eval harness on the agentic set
## (run from the data dir so its RESULTS.md lands there, not clobbering root)
agentic-eval: build
	cd $(DATA_AGENTIC) && AIL_DATA_DIR=. ../$(BIN)/eval

## agentic-fit-eval: downstream pipeline once labels exist — materialize the
## engine's canonical labels into datasets, then fit + evaluate. Run after the
## label branches (agentic-grade / agentic-heuristics / agentic-calibrate) have
## produced labels/resolved.jsonl.
agentic-fit-eval: agentic-materialize agentic-train agentic-eval
	@echo "== agentic fit+eval done. see $(DATA_AGENTIC)/RESULTS.md =="

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
	cd $(DATA_AGENTIC) && AIL_DATA_DIR=. ../$(BIN)/eval
	@# 4) write RESULTS_AGENTIC.md
	python3 agentic/runner/report.py
	@echo ""
	@echo "== agentic done. see RESULTS_AGENTIC.md and $(DATA_AGENTIC)/ =="
