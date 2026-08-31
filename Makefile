.DEFAULT_GOAL := help
.PHONY: help hooks preflight test lint docs-check ci verify-phase-0

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

verify-phase-0: ## Phase 0 gate: preflight is advisory here, tests and docs are not
	@bash scripts/preflight.sh || echo "preflight reported problems, continuing (phase 0 needs no docker and no keys)"
	@$(MAKE) --no-print-directory test docs-check
