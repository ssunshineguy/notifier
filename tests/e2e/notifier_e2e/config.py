NOTIFIER_API_URL = "http://127.0.0.1:8080"
EMULATOR_URL = "http://127.0.0.1:8081"
CALLBACK_URL = "http://emulator:8081/callback"

TIMEOUT_SECONDS = 0.5
DATABASE_URL = "postgresql+psycopg://notifier_e2e:notifier_e2e@127.0.0.1:5432/notifier?sslmode=disable"
DATABASE_CONNECT_ARGS = {
    "connect_timeout": 2,  # Psycopg enforces a two-second minimum for connecting.
    "options": f"-c statement_timeout={int(TIMEOUT_SECONDS * 1000)}",
}
