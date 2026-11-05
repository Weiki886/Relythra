GO_PACKAGES := ./cmd/... ./internal/... ./migrations/...
GO_FILES := $$(find cmd internal migrations -type f -name '*.go')
COMPOSE := docker compose --file deploy/compose/compose.yaml

.PHONY: fmt fmt-check vet test build web-install web-check compose-config smoke demo-smoke check

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@unformatted="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$unformatted" ]; then \
		printf 'The following Go files need gofmt:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet $(GO_PACKAGES)

test:
	go test $(GO_PACKAGES)

build:
	go build ./cmd/...

web-install:
	npm --prefix web ci

web-check:
	npm --prefix web run lint
	npm --prefix web run typecheck
	npm --prefix web test -- --run
	npm --prefix web run build

compose-config:
	$(COMPOSE) config --quiet

smoke:
	./scripts/smoke.sh

demo-smoke:
	./scripts/demo-smoke.sh

check: fmt-check vet test build web-check compose-config
