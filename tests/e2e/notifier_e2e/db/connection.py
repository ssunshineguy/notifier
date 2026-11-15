from sqlalchemy import create_engine
from sqlalchemy.engine import Engine

from notifier_e2e import config


def create_test_engine() -> Engine:
    return create_engine(
        config.DATABASE_URL,
        connect_args=config.DATABASE_CONNECT_ARGS,
    )
