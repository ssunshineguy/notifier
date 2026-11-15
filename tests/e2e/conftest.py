from collections.abc import Iterator
from uuid import uuid4

import pytest
from requests import Session
from sqlalchemy.engine import Engine

from notifier_e2e import config
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.connection import create_test_engine
from notifier_e2e.db.notification import Notification


@pytest.fixture(scope="session")
def database() -> Iterator[Engine]:
    engine = create_test_engine()
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
def notifier_api(client: Client) -> Iterator[NotifierApiClient]:
    with Session() as session:
        yield NotifierApiClient(session, client.client_id)
