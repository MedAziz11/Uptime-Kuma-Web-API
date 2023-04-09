build:
	docker buildx build --platform linux/arm64,linux/arm/v7,linux/amd64 --push -t nikoogle:uptimekuma_restapi .

inspect:
	docker buildx inspect --bootstrap

push:
	docker push nikoogle:uptimekuma_restapi

up:
	docker compose up --build

test:
	bash tests/monitor.sh
	bash tests/statuspage.sh