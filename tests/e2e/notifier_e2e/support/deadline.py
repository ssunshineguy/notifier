import math
import signal
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from types import FrameType

from requests import Timeout


@contextmanager
def request_deadline(timeout_seconds: float | None) -> Iterator[None]:
    if timeout_seconds is None:
        yield
        return
    if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
        raise ValueError("HTTP request budget must be finite and positive")
    if threading.current_thread() is not threading.main_thread():
        raise RuntimeError("Bounded HTTP requests require the main thread")
    if not hasattr(signal, "setitimer") or not hasattr(signal, "ITIMER_REAL"):
        raise RuntimeError("Bounded HTTP requests require POSIX interval timers")
    if any(signal.getitimer(signal.ITIMER_REAL)):
        raise RuntimeError("Bounded HTTP requests cannot replace an active interval timer")

    def expire(_signum: int, _frame: FrameType | None) -> None:
        raise Timeout("HTTP request deadline exceeded")

    previous_handler = signal.getsignal(signal.SIGALRM)
    signal.signal(signal.SIGALRM, expire)
    try:
        signal.setitimer(signal.ITIMER_REAL, timeout_seconds)
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous_handler)
