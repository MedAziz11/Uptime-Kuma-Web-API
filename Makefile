VERSION ?= 3.0.0-dev
IMAGE   ?= medaziz11/uptimekuma_restapi:$(VERSION)
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION   ?= v1.9.0
LDFLAGS := -s -w -X github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/version.Version=$(VERSION)

.PHONY: fmt vet lint test bench build run docker up down e2e

fmt:
	gofmt -w .

vet:
	go vet ./...

# golangci-lint and govulncheck at the versions CI uses
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

test:
	go test -race -cover ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o uptime-kuma-api ./cmd/uptime-kuma-api

# Needs the variables of .env.dist in the environment.
run: build
	./uptime-kuma-api

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

up: down
	docker compose up --build

down:
	docker compose down --remove-orphans

# Acceptance tests against a real Uptime Kuma (KUMA_VERSION, default 2). Needs Docker,
# Python with tests/e2e/requirements.txt and a .env file (KUMA_USERNAME, KUMA_PASSWORD,
# ADMIN_PASSWORD). Removes the stack and its volumes afterwards.
e2e:
	docker compose up -d --build --wait
	set -a && . ./.env && set +a && \
	python tests/e2e/setup_kuma.py http://127.0.0.1:3001 && \
	E2E_API_URL=http://127.0.0.1:8000 E2E_ADMIN_PASSWORD="$$ADMIN_PASSWORD" python -m pytest tests/e2e; \
	status=$$?; docker compose down -v; exit $$status
