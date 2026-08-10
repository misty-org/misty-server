.DEFAULT_GOAL := help
.PHONY: help build run test test-unit lint fmt fmt-check vet sizes migrate check

GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo "$(shell go env GOPATH)/bin/golangci-lint")

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Compile all packages
	go build ./...

run: ## Run the API server
	go run ./cmd/misty-server

test: ## Full suite — bootstraps Docker Postgres on :5435
	./test.sh

test-unit: ## Go tests only (needs Postgres already up)
	go test -p 1 ./... -count=1

lint: ## golangci-lint
	@test -x "$(GOLANGCI)" || { \
		echo "golangci-lint not found. Install it with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 1; }
	"$(GOLANGCI)" run

fmt: ## Format all Go source
	gofmt -w .

fmt-check: ## Fail if any file is unformatted
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "$$files"; exit 1; fi

vet: ## go vet
	go vet ./...

sizes: ## Enforce the 500-line file cap
	./scripts/check-go-file-sizes.sh

migrate: ## Apply database migrations
	./scripts/goose.sh up

check: fmt-check vet sizes lint test ## Everything CI runs
