.DEFAULT_GOAL := help
.PHONY: help hooks preflight test lint docs-check ci verify-phase-0 \
	verify-offline jaeger-up jaeger-down seed

help: ## Show this help
	@grep -hE '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t20

hooks: ## Point git at scripts/hooks so the pre-commit gate runs
	@git config core.hooksPath scripts/hooks && echo "core.hooksPath = scripts/hooks"

preflight: ## Check toolchain, docker, and credentials
	@bash scripts/preflight.sh

test: ## Run the Go tests
	@go test ./...

lint: ## gofmt and go vet
	@test -z "$$(gofmt -l . | tee /dev/stderr)" && go vet ./...

docs-check: ## Run the prose gate over every tracked .md and .txt
	@bash scripts/check-docs.sh

ci: lint test docs-check ## What CI runs

jaeger-up: ## Start Jaeger and wait for its query API
	@bash scripts/jaeger-up.sh

jaeger-down: ## Stop Jaeger and remove its volumes
	@bash scripts/jaeger-down.sh

seed: ## Seed a batch and write its manifest (pending the cmd/rzp subcommand)
	@bash scripts/seed-batch.sh

verify-phase-0: ## Phase 0 gate: preflight is advisory here, tests and docs are not
	@bash scripts/preflight.sh || echo "preflight reported problems, continuing (phase 0 needs no docker and no keys)"
	@$(MAKE) --no-print-directory test docs-check

verify-offline: ## Phase 1 offline gate: whole suite, no keys, no docker
	@bash scripts/preflight.sh || echo "preflight reported problems, continuing (the offline half needs no docker and no keys)"
	@env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET $(MAKE) --no-print-directory lint test docs-check
