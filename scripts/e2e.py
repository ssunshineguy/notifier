#!/usr/bin/env python3
"""Install, run, or explicitly update the pinned e2e environment."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parent.parent
PROJECT = ROOT / "tests/e2e"
CONSTRAINTS = PROJECT / "constraints.txt"


def run(*args, **kwargs):
    subprocess.run(args, cwd=ROOT, check=True, **kwargs)


def check_python():
    expected = (ROOT / ".python-version").read_text().strip()
    actual = ".".join(map(str, sys.version_info[:3]))
    if actual != expected:
        raise SystemExit(f"Python {expected} required, got {actual}; select it with PYTHON.")


def verify():
    from importlib.metadata import PackageNotFoundError, version
    try:
        from packaging.requirements import Requirement
        import notifier_e2e
    except ImportError:
        raise SystemExit("Incomplete e2e environment; run make setup-e2e.") from None

    if Path(notifier_e2e.__file__).resolve().parent != PROJECT / "notifier_e2e":
        raise SystemExit("The e2e environment belongs to another checkout; run make setup-e2e.")

    for line in CONSTRAINTS.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        requirement = Requirement(line)
        if requirement.marker and not requirement.marker.evaluate():
            continue
        try:
            installed = version(requirement.name)
        except PackageNotFoundError:
            raise SystemExit(f"Missing {requirement.name}; run make setup-e2e.") from None
        if installed not in requirement.specifier:
            raise SystemExit(f"{requirement.name}=={installed} differs from lock; run make setup-e2e.")
    run(sys.executable, "-m", "pip", "--isolated", "check")


def main():
    check_python()
    action = sys.argv[1]
    venv = Path(os.environ.get("E2E_VENV", str(PROJECT / ".venv"))).resolve()
    python = str(venv / "bin/python")
    if action == "setup":
        run(sys.executable, "-m", "venv", str(venv))
        run(python, "-m", "pip", "--isolated", "--disable-pip-version-check", "install",
            "--constraint", str(CONSTRAINTS), "pip")
        run(python, "-m", "pip", "--isolated", "--disable-pip-version-check", "install",
            "--constraint", str(CONSTRAINTS), "--build-constraint", str(CONSTRAINTS),
            "--requirement", str(CONSTRAINTS), "--editable", str(PROJECT))
        run(python, str(Path(__file__).resolve()), "verify")
    elif action == "test":
        if not Path(python).is_file():
            raise SystemExit("Missing e2e environment; run make setup-e2e first.")
        run(python, str(Path(__file__).resolve()), "verify")
        run(python, "-m", "pytest", "tests/e2e", *sys.argv[2:])
    elif action == "lock":
        import tomllib

        with tempfile.TemporaryDirectory(prefix="notifier-e2e-lock-") as directory:
            run(sys.executable, "-m", "venv", directory)
            compiler = str(Path(directory) / "bin/python")
            run(compiler, "-m", "pip", "--isolated", "--disable-pip-version-check", "install",
                *tomllib.loads((PROJECT / "pyproject.toml").read_text())["project"]["optional-dependencies"]["tooling"])
            run(compiler, "-m", "piptools", "compile", "--upgrade", "--all-build-deps",
                "--all-extras", "--strip-extras", "--allow-unsafe", "--no-emit-index-url",
                "--no-emit-trusted-host", "--output-file", "tests/e2e/constraints.txt",
                "tests/e2e/pyproject.toml",
                env={**os.environ, "CUSTOM_COMPILE_COMMAND": "make lock-e2e"})
    elif action == "verify":
        verify()
    else:
        raise SystemExit(f"Unknown action: {action}")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(error.returncode) from None
