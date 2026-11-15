from sqlalchemy import create_engine
from sqlalchemy.engine import Engine

from notifier_e2e import config


def create_test_engine(database_url: str) -> Engine:
    return create_engine(
        database_url,
        connect_args=config.DATABASE_CONNECT_ARGS,
    )
