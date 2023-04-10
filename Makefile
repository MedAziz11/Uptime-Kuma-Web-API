up: down
	docker compose up --build

down:
	docker compose down --remove-orphans

test:
	bash tests/monitor.sh
	bash tests/statuspage.sh
