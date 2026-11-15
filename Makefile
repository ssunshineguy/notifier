.PHONY: gen up down reset migrate psql ai-setup ai-check check test-integration setup-e2e test-e2e lock-e2e

PYTHON ?= python3.14
E2E_VENV ?= tests/e2e/.venv
export E2E_VENV

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

check:
	go test ./...
	go vet ./...
	golangci-lint run
	@diff=$$(mktemp); trap 'rm -f "$$diff"' EXIT; \
		golangci-lint fmt --diff > "$$diff" || { cat "$$diff"; exit 1; }; \
		if test -s "$$diff"; then cat "$$diff"; exit 1; fi
	git diff --check

test-integration:
	go test -tags=integration -count=1 ./notifier/internal/storage/postgres

setup-e2e:
	$(PYTHON) scripts/e2e.py setup

test-e2e:
	$(PYTHON) scripts/e2e.py test $(PYTEST_ARGS)

lock-e2e:
	$(PYTHON) scripts/e2e.py lock
