.DEFAULT_GOAL := help
.PHONY: help hooks preflight test test-integration lint docs-check ci \
	verify-phase-0 verify-offline verify-live jaeger-up jaeger-down seed \
	auth-probe capture demo

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

test: ## Run the Go tests
	@go test ./...

test-integration: ## Run the live test-mode tests. Spends real API calls.
	@$(RUN_WITH_ENV) env RZP_CONTRACT_HARNESSES=live go test -tags=integration -count=1 ./internal/razorpay/

lint: ## gofmt and go vet, including the integration-tagged files
	@test -z "$$(gofmt -l . | tee /dev/stderr)" && go vet ./... && go vet -tags=integration ./...

docs-check: ## Run the prose gate over every tracked .md and .txt
	@bash scripts/check-docs.sh

ci: lint test docs-check ## What CI runs

jaeger-up: ## Start Jaeger and wait for its query API
	@bash scripts/jaeger-up.sh

jaeger-down: ## Stop Jaeger and remove its volumes
	@bash scripts/jaeger-down.sh

seed: ## Seed a batch and write its manifest (pending the cmd/rzp subcommand)
	@bash scripts/seed-batch.sh

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
