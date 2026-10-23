.DEFAULT_GOAL := help
.PHONY: help generate console build run test test-unit lint fmt fmt-check vet sizes migrate check

GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo "$(shell go env GOPATH)/bin/golangci-lint")

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

generate: ## Regenerate templ views for the operator console
	go tool templ generate -path internal/console

console: generate ## Run the operator console (prefer: misty server up --gui)
	go run ./cmd/misty-console

build: ## Compile all packages
	go build ./...

run: ## Run the API server
	go run ./cmd/misty-server

test: ## Full suite — bootstraps Docker Postgres on :5435
	./test.sh

test-unit: ## Every Go test without the Docker bootstrap (DB tests need Postgres already up)
	go test -p 1 ./... -count=1

lint: ## golangci-lint
	@test -x "$(GOLANGCI)" || { \
		echo "golangci-lint not found. Install it with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 1; }
	"$(GOLANGCI)" run

# gofmt writes LF, and many files here use CRLF. Both targets strip CR before
# formatting, and fmt writes CRLF files back with CRLF.
GO_SOURCES = find . -name '*.go' -not -path '*/node_modules/*'

fmt: ## Format all Go source, keeping each file's line endings
	@$(GO_SOURCES) | while read -r f; do \
		if grep -q "$$(printf '\r')" "$$f"; then \
			tr -d '\r' < "$$f" | gofmt | awk '{printf "%s\r\n", $$0}' > "$$f.fmt" || { rm -f "$$f.fmt"; exit 1; }; \
			if cmp -s "$$f" "$$f.fmt"; then rm "$$f.fmt"; else mv "$$f.fmt" "$$f"; fi; \
		else gofmt -w "$$f"; fi; \
	done

fmt-check: ## Fail if any file is unformatted (CRLF line endings are fine)
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	$(GO_SOURCES) | while read -r f; do \
		mkdir -p "$$tmp/$$(dirname "$$f")"; tr -d '\r' < "$$f" > "$$tmp/$$f"; \
	done; \
	files="$$(cd "$$tmp" && gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "$$files"; exit 1; fi

vet: ## go vet
	go vet ./...

sizes: ## Enforce the 500-line file cap
	misty check go-sizes

migrate: ## Apply database migrations
	./scripts/goose.sh up

check: fmt-check vet sizes lint test ## Every check to run before pushing (there is no CI)
