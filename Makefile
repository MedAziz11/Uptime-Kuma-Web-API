.PHONY: up down setup run lint format test coverage e2e

up: down
	docker compose up --build

down:
	docker compose down --remove-orphans

setup:
	python3 -m venv .venv && \
	. .venv/bin/activate && \
	pip install -r requirements-dev.txt

run:
	uvicorn app.main:create_app --factory --reload

lint:
	ruff check app tests
	ruff format --check app tests

format:
	ruff check --fix app tests
	ruff format app tests

test:
	pytest

coverage:
	pytest --cov --cov-report=term --cov-report=html

# End-to-end tests against a real Uptime Kuma. Needs Docker and a .env file
# (KUMA_USERNAME, KUMA_PASSWORD, ADMIN_PASSWORD). Removes the stack and its volumes afterwards.
e2e:
	docker compose up -d --build --wait
	set -a && . ./.env && set +a && \
	python tests/e2e/setup_kuma.py http://127.0.0.1:3001 && \
	E2E_API_URL=http://127.0.0.1:8000 E2E_ADMIN_PASSWORD="$$ADMIN_PASSWORD" pytest tests/e2e; \
	status=$$?; docker compose down -v; exit $$status
