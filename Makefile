.PHONY: gen up down reset migrate psql ai-setup

gen:
	./scripts/gen.sh

up:
	docker compose up --build --wait

down:
	docker compose down

reset:
	docker compose down -v

migrate:
	docker compose run --rm --build migrate
	./scripts/schema-dump.sh

psql:
	docker compose exec postgres psql -U notifier_migrator -d notifier

ai-setup:
	./scripts/ai-setup.sh
