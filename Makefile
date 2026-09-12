.PHONY: up down logs test load metrics

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f app redis

test:
	docker build --target build -t pulseboard-test .
	docker run --rm pulseboard-test go test ./...

load:
	docker compose --profile load run --rm k6

metrics:
	docker compose --profile observability up -d prometheus
