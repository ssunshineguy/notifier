.PHONY: gen up down reset migrate psql ai-setup ai-check

gen:
	./scripts/gen.sh

up:
	docker compose up --build --wait
	docker compose exec -T postgres psql -q -U postgres -d notifier -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/002_e2e_role.sql
	@set -e; \
		api=$$(docker compose port api 8080); \
		emulator=$$(docker compose port emulator 8081); \
		postgres=$$(docker compose port postgres 5432); \
		printf 'Notifier API: http://%s\nEmulator: http://%s\nPostgreSQL: %s\n' "$$api" "$$emulator" "$$postgres"

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

ai-check:
	python3 -B scripts/ai-check.py
