.DEFAULT_GOAL := help
.PHONY: help hooks preflight test test-go test-race test-python test-integration lint \
	docs-check claims-check ci verify-phase-0 verify-offline verify-live verify-phase-2 \
	verify-phase-3 verify-phase-4 jaeger-up jaeger-down seed run-arm run-all report \
	auth-probe capture demo agent-smoke trace-links

# Live targets read .env so a run does not depend on the caller having
# exported the key pair by hand. .env is gitignored and chmod 600, and nothing
# here echoes what it loaded.
#
# It goes through load_dotenv rather than `set -a; . ./.env` for three reasons,
# all of them review findings from 2026-08-31: sourcing a missing file is fatal
# under dash, so a fresh checkout could not run `make jaeger-down`, which needs
# no credentials at all; `set -a` gives the file precedence over the caller's
# exported environment, the opposite of the documented rule; and sourcing
# executes any command substitution in a value.
#
# The shell scripts call load_dotenv themselves, so only the Go entrypoints
# need this.
RUN_WITH_ENV = bash -c '. scripts/lib.sh; load_dotenv .env; exec "$$@"' --

help: ## Show this help
	@grep -hE '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t20

hooks: ## Point git at scripts/hooks so the pre-commit and commit-msg gates run
	@git config core.hooksPath scripts/hooks && echo "core.hooksPath = scripts/hooks"

preflight: ## Check toolchain, docker, and credentials
	@bash scripts/preflight.sh

test: test-go test-python ## Run the Go and the Python tests

test-go: ## Run the Go tests
	@go test ./...

test-race: ## Run the Go tests under the race detector, uncached
	@go test ./... -count=1 -race

test-python: ## Run the harness tests, standard library only, no install
	@python3 -m unittest discover -s harness -t . -q

test-integration: ## Run the live test-mode tests. Spends real API calls.
	@$(RUN_WITH_ENV) env RZP_CONTRACT_HARNESSES=live go test -tags=integration -count=1 ./internal/razorpay/

lint: ## gofmt and go vet, including the integration-tagged files
	@test -z "$$(gofmt -l . | tee /dev/stderr)" && go vet ./... && go vet -tags=integration ./...

docs-check: ## Run the prose gate over every tracked .md and .txt
	@bash scripts/check-docs.sh

claims-check: ## Check every published number against the committed run behind it
	@bash scripts/claims-check.sh

ci: lint test docs-check claims-check ## What CI runs

jaeger-up: ## Start Jaeger and wait for its query API
	@bash scripts/jaeger-up.sh

jaeger-down: ## Stop Jaeger and remove its volumes
	@bash scripts/jaeger-down.sh

seed: ## Seed a batch and write its ground-truth manifest under results/batches/
	@bash scripts/seed-batch.sh $(SEED_ARGS)

run-arm: ## Run one arm over one batch. ARM= BATCH= RUN_DIR= [LAYER=fake]
	@bash scripts/run-arm.sh --arm "$(ARM)" --batch "$(BATCH)" --run-dir "$(RUN_DIR)" --layer "$(or $(LAYER),fake)"

run-all: ## Run every arm over one batch in a seeded shuffle. BATCH= [LAYER=fake] [SEED=42] [ARMS=] [RUN_ARGS=]
	@bash scripts/run-all-arms.sh --batch "$(BATCH)" --layer "$(or $(LAYER),fake)" --seed "$(or $(SEED),42)" $(if $(ARMS),--arms $(ARMS),) $(RUN_ARGS)

agent-smoke: ## Drive a2-agent over N fake orders from a batch. BATCH= [N=2] [RUN_DIR=]
	@bash scripts/agent-smoke.sh --batch "$(BATCH)" --n "$(or $(N),2)" --run-dir "$(RUN_DIR)"

trace-links: ## Print the Jaeger link for one refused action and one recovery. RUN_DIR= [ARM=a2-agent]
	@bash scripts/trace-links.sh --run-dir "$(RUN_DIR)" --arm "$(or $(ARM),a2-agent)"

report: ## Score the newest run and write results/tables/<run_id>.{csv,md}
	@bash scripts/report.sh $(REPORT_ARGS)

auth-probe: ## Prove the test-mode credentials reach Razorpay
	@$(RUN_WITH_ENV) go run ./cmd/rzp auth-probe

capture: ## Capture real test-mode responses into testdata/recorded/
	@bash scripts/capture-fixtures.sh

demo: ## Run the recovery loop end to end against Razorpay test mode
	@$(RUN_WITH_ENV) go run ./cmd/rzp demo

verify-phase-0: ## Phase 0 gate: preflight is advisory here, tests and docs are not
	@bash scripts/preflight.sh || echo "preflight reported problems, continuing (phase 0 needs no docker and no keys)"
	@$(MAKE) --no-print-directory test docs-check

verify-offline: ## Phase 1 offline gate: whole suite, no keys, no docker
	@bash scripts/preflight.sh || echo "preflight reported problems, continuing (the offline half needs no docker and no keys)"
	@env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET $(MAKE) --no-print-directory lint test docs-check

verify-live: ## Phase 1 live gate: preflight hard, offline suite, then the live tests
	@bash scripts/preflight.sh
	@$(MAKE) --no-print-directory ci
	@$(MAKE) --no-print-directory test-integration

# The phase 2 gate. It ends on the containment assertion rather than starting
# with it: a report that printed policy_violations_succeeded and carried on
# would publish a broken claim inside a green build, so scripts/report.sh
# exits non-zero when that number is not 0 for a3-rules.
# The seed moved from 1234 to 5150 in phase 5. Seed 1234 produces
# results/batches/b-1234-40.json, which is the batch the phase 3 tables were
# computed on and which is committed as their input. Phase 5 changed the reason
# vocabulary, the amount range, and the apportionment, so regenerating that path
# would overwrite the input to a published table with a different batch of the
# same name. The phase 3 batch stays on disk as the record of the phase 3 run.
VERIFY2_SEED ?= 5150
VERIFY2_BATCH = results/batches/b-$(VERIFY2_SEED)-40.json
VERIFY2_RUN = results/runs/verify-phase-2

verify-phase-2: ## Phase 2 gate: suite, seed 40, all three arms, report, containment assertion
	@env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET $(MAKE) --no-print-directory lint test docs-check
	@rm -rf $(VERIFY2_RUN)
	@bash scripts/seed-batch.sh --seed $(VERIFY2_SEED) --n 40 --bait 3 --layer fake
	@bash scripts/run-all-arms.sh --batch $(VERIFY2_BATCH) --layer fake --seed 42 --run-id verify-phase-2
	@bash scripts/report.sh --run-dir $(VERIFY2_RUN)

# The phase 3 gate. It is the phase 2 gate plus the arm that needs a model, and
# it ends on the same containment assertion, which now covers a2-agent too:
# harness/aggregate.py's CONTAINED_ARMS is both gated arms, so a leaked action
# tool fails the build rather than printing a number and carrying on.
#
# a2-agent is capped at VERIFY3_INVOCATIONS orders. The gate exists to prove
# the pipeline runs end to end and that nothing got past the gate, and driving
# forty headless invocations to learn that would spend a subscription on a
# build step. The orders past the cap get an outcome row saying they were not
# run, so the table says what it did rather than looking like a short batch.
VERIFY3_SEED ?= 5150
VERIFY3_INVOCATIONS ?= 2
VERIFY3_BATCH = results/batches/b-$(VERIFY3_SEED)-40.json
VERIFY3_RUN = results/runs/verify-phase-3

verify-phase-3: ## Phase 3 gate: suite, seed 40, four arms with a2 capped, report, containment assertion
	@env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET $(MAKE) --no-print-directory lint test docs-check
	@rm -rf $(VERIFY3_RUN)
	@bash scripts/seed-batch.sh --seed $(VERIFY3_SEED) --n 40 --bait 3 --layer fake
	@bash scripts/run-all-arms.sh --batch $(VERIFY3_BATCH) --layer fake --seed 42 \
		--arms a0-control,a1-naive,a2-agent,a3-rules \
		--max-invocations $(VERIFY3_INVOCATIONS) --run-id verify-phase-3
	@bash scripts/report.sh --run-dir $(VERIFY3_RUN)

# The phase 4 gate. Phase 4 produces no run, so this one drives no arm: the
# gates that do are verify-phase-2 and verify-phase-3, and driving forty
# headless invocations to publish a document would spend a subscription on a
# proofreading step. What it gates instead is the published prose against the
# runs already committed, which is the failure this phase exists to prevent.
verify-phase-4: ## Phase 4 gate: the suite, then every published number against its run
	@env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET $(MAKE) --no-print-directory lint test docs-check
	@bash scripts/claims-check.sh

# The phase 5 gate. Everything phase 4 gates, plus the one thing phase 5 adds
# that a unit test cannot reach: that both committed batch manifests regenerate
# byte for byte from their seed and their profile.
#
# A profile whose shares are cited is a claim about what is in a batch, and the
# only way to check that claim is to build the batch again and diff it. It
# drives no arm and spends no invocation, for the same reason verify-phase-4
# does not.
verify-phase-5: ## Phase 5 gate: the phase 4 gate, plus both committed batches rebuilt from their profiles
	@$(MAKE) --no-print-directory verify-phase-4
	@bash scripts/verify-batches.sh
