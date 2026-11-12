.PHONY: up down setup run lint format test e2e

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

# Needs the stack running (make up), jq, and ADMIN_PASSWORD in the environment.
e2e:
	bash tests/e2e/monitor.sh
	bash tests/e2e/maintenance.sh
	bash tests/e2e/statuspage.sh
