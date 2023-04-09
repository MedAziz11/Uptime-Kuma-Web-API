up:
	docker compose up --build

test:
	bash tests/monitor.sh
	bash tests/statuspage.sh
