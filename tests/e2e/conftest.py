from collections.abc import Callable, Iterator
from contextlib import ExitStack
from pathlib import Path
from uuid import uuid4

import pytest
from requests import Session
from sqlalchemy.engine import Engine

pytest.register_assert_rewrite(
    "notifier_e2e.support.matchers", "notifier_e2e.steps.notifications"
)

from notifier_e2e import config
from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.connection import create_test_engine
from notifier_e2e.db.notification import Notification


@pytest.fixture(scope="session")
def endpoints() -> config.ComposeEndpoints:
    return config.compose_endpoints(Path(__file__).resolve().parents[2])


@pytest.fixture(scope="session")
def database(endpoints: config.ComposeEndpoints) -> Iterator[Engine]:
    engine = create_test_engine(endpoints.database_url)
    try:
        yield engine
    finally:
        engine.dispose()


@pytest.fixture
def client_factory(
    database: Engine, endpoints: config.ComposeEndpoints
) -> Iterator[Callable[[], Client]]:
    with Session() as session, ExitStack() as cleanup:
        emulator = EmulatorClient(session, base_url=endpoints.emulator_url)

        def cleanup_client(created: Client) -> None:
            try:
                emulator.reset_client_overrides(created.client_id).raise_for_status()
            finally:
                with database.begin() as connection:
                    Notification.delete_for_client(connection, created.client_id)
                    created.delete(connection)

        def create_client() -> Client:
            created = Client(
                client_id=f"e2e-{uuid4().hex}",
                callback_url=config.CALLBACK_URL,
                max_lifetime_sec=3600,
            )
            with database.begin() as connection:
                created.insert(connection)
            cleanup.callback(cleanup_client, created)
            return created

        yield create_client


@pytest.fixture
def client(client_factory: Callable[[], Client]) -> Client:
    return client_factory()


@pytest.fixture
def notifier_api_factory(
    endpoints: config.ComposeEndpoints,
) -> Iterator[Callable[[Client], NotifierApiClient]]:
    with Session() as session:
        yield lambda client: NotifierApiClient(
            session, client.client_id, base_url=endpoints.api_url
        )


@pytest.fixture
def notifier_api(
    client: Client, notifier_api_factory: Callable[[Client], NotifierApiClient]
) -> NotifierApiClient:
    return notifier_api_factory(client)


@pytest.fixture
def emulator_api(endpoints: config.ComposeEndpoints) -> Iterator[EmulatorClient]:
    with Session() as session:
        yield EmulatorClient(session, base_url=endpoints.emulator_url)
