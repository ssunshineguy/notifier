from collections.abc import Iterator
from pathlib import Path
from uuid import uuid4

import pytest
from requests import Session
from sqlalchemy.engine import Engine

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
def client(database: Engine) -> Iterator[Client]:
    client = Client(
        client_id=f"e2e-{uuid4().hex}",
        callback_url=config.CALLBACK_URL,
        max_lifetime_sec=3600,
    )
    with database.begin() as connection:
        client.insert(connection)

    try:
        yield client
    finally:
        with database.begin() as connection:
            Notification.delete_for_client(connection, client.client_id)
            client.delete(connection)


@pytest.fixture
def notifier_api(client: Client, endpoints: config.ComposeEndpoints) -> Iterator[NotifierApiClient]:
    with Session() as session:
        yield NotifierApiClient(session, client.client_id, base_url=endpoints.api_url)


@pytest.fixture
def emulator_api(client: Client, endpoints: config.ComposeEndpoints) -> Iterator[EmulatorClient]:
    with Session() as session:
        emulator = EmulatorClient(session, base_url=endpoints.emulator_url)
        try:
            yield emulator
        finally:
            emulator.reset_client_overrides(client.client_id).raise_for_status()
