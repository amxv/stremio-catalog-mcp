SHELL := /bin/bash

.PHONY: fmt test vet check docs-install docs-dev docs-check docs-build docs-preview

fmt:
	@gofmt -w $$(find api pkg -name '*.go')

test:
	@go test ./...

vet:
	@go vet ./...

check: test vet

docs-install:
	@cd docs && bun install --frozen-lockfile

docs-dev:
	@cd docs && bun run dev

docs-check:
	@cd docs && bun run check

docs-build:
	@cd docs && bun run build

docs-preview:
	@cd docs && bun run preview
