from dataclasses import dataclass

from sqlalchemy import column, table
from sqlalchemy.engine import Connection

clients = table(
    "t_client",
    column("client_id"),
    column("callback_url"),
    column("max_lifetime_sec"),
    schema="notifier",
)


@dataclass(frozen=True)
class Client:
    client_id: str
    callback_url: str
    max_lifetime_sec: int

    def insert(self, connection: Connection) -> None:
        connection.execute(
            clients.insert().values(
                client_id=self.client_id,
                callback_url=self.callback_url,
                max_lifetime_sec=self.max_lifetime_sec,
            )
        )

    def delete(self, connection: Connection) -> None:
        connection.execute(clients.delete().where(clients.c.client_id == self.client_id))
