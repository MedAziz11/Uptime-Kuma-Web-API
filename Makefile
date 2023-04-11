up: down
	docker compose up --build

down:
	docker compose down --remove-orphans

test:
	bash tests/monitor.sh
	bash tests/maintenance.sh
	bash tests/statuspage.sh

setup:
	python3 -m venv venv && \
	. venv/bin/activate && \
	pip install -r requirements.txt
