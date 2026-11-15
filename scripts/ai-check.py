#!/usr/bin/env python3
# Checks canonical AI metadata and setup drift without updating the checkout.
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit

try:
    import tomllib
except ImportError:
    raise SystemExit("ai-check requires Python 3.11 or newer")


OUTPUTS = (
    ".claude/rules", ".claude/agents", ".claude/skills",
    ".agents/skills", ".codex/agents",
)
REQUIRED_CASES = {
    "python-only", "emulator-201", "migration-locks", "readme-drift", "accepted-warn",
    "useful-dnd", "moved-dnd", "removed-dnd", "unmarked-dnd",
}


def scalar(value: str | None, key: str) -> str:
    if value and value.startswith('"'):
        parsed = json.loads(value)
        if isinstance(parsed, str) and parsed.strip():
            return parsed
    elif value and value.startswith("'"):
        if re.fullmatch(r"'(?:[^']|'')*'", value) and value[1:-1].strip():
            return value[1:-1].replace("''", "'")
    elif value and not re.search(r":\s|\s#|^[\[\]{}&*!>|%@`#,]|^[-?:](?:\s|$)", value):
        non_string = re.fullmatch(
            r"(?i:null|~|true|false|yes|no|on|off|[-+]?\.(?:inf|nan))"
            r"|[-+]?(?:[0-9][0-9_]*(?:\.[0-9_]*)?|\.[0-9_]+)(?:[eE][-+]?[0-9]+)?"
            r"|[-+]?0[xob][0-9a-fA-F_]+|[0-9]+(?::[0-9]+)+"
            r"|[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[Tt ].*)?",
            value,
        )
        if not non_string:
            return value
    raise ValueError(f"empty or unsupported scalar: {key}")


def frontmatter(path: Path, kind: str) -> tuple[dict, str]:
    text = path.read_text()
    if not text.startswith("---\n") or "\n---\n" not in text[4:]:
        raise ValueError("missing frontmatter delimiters")
    header, body = text[4:].split("\n---\n", 1)
    fields: dict[str, object] = {}
    current = ""
    for line in header.splitlines():
        if line.startswith("  - ") and current == "paths":
            if not isinstance(fields[current], list):
                raise ValueError("paths must be a block list")
            fields[current].append(scalar(line[4:], "paths item"))
            continue
        match = re.fullmatch(r"([A-Za-z][\w-]*):(?: (.*))?", line)
        if not match:
            raise ValueError(f"unsupported frontmatter syntax: {line!r}")
        current, value = match.groups()
        if current in fields:
            raise ValueError(f"duplicate frontmatter key: {current}")
        if current == "paths" and value is None:
            fields[current] = []
        elif current == "alwaysApply":
            if value not in ("true", "false"):
                raise ValueError("alwaysApply must be an unquoted true or false")
            fields[current] = value == "true"
        else:
            fields[current] = scalar(value, current)
    required = {"description", "globs", "alwaysApply"} if kind == "rule" else {"name", "description"}
    allowed = required | ({"paths"} if kind == "rule" else {"tools", "model"} if kind == "agent" else set())
    if required - fields.keys() or fields.keys() - allowed:
        raise ValueError(f"frontmatter keys: required {sorted(required)}, allowed {sorted(allowed)}")
    for key, value in fields.items():
        if key == "paths":
            if not isinstance(value, list) or not value or any(not item.strip() for item in value):
                raise ValueError("paths must be a nonempty block list")
        elif key != "alwaysApply" and (not isinstance(value, str) or not value.strip()):
            raise ValueError(f"{key} must be a nonempty string")
    if kind != "rule":
        expected = path.parent.name if kind == "skill" else path.stem
        if fields["name"] != expected or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", expected):
            raise ValueError(f"name must match {expected}")
    if kind == "agent" and (fields.get("tools") != "Read, Grep, Glob" or fields.get("model") != "inherit"):
        raise ValueError("agent must declare read-only tools Read, Grep, Glob and model inherit")
    if not body.strip():
        raise ValueError("missing instructions after frontmatter")
    return fields, body.strip()


def prose(text: str) -> str:
    lines = []
    fence = None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            value = marker[1]
            if fence is None:
                fence = value
            elif value[0] == fence[0] and len(value) >= len(fence):
                fence = None
            lines.append("")
        else:
            lines.append(line if fence is None else "")
    return "\n".join(lines)


def anchors(path: Path) -> set[str]:
    text = prose(path.read_text())
    result = set(re.findall(r'<(?:a|[hH][1-6])\s+(?:[^>]*?\s)?(?:id|name)=["\']([^"\']+)', text))
    for heading in re.findall(r"^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$", text, re.MULTILINE):
        slug = re.sub(r"[^\w\- ]", "", re.sub(r"<[^>]+>", "", heading).lower()).replace(" ", "-")
        candidate = slug
        suffix = 0
        while candidate in result:
            suffix += 1
            candidate = f"{slug}-{suffix}"
        result.add(candidate)
    return result


def check_links(root: Path, path: Path, errors: list[str]) -> None:
    text = re.sub(r"`+[^`\n]*`+", "", prose(path.read_text()))
    references = dict(re.findall(r"^\s{0,3}\[([^\]]+)\]:\s*(\S+)", text, re.MULTILINE))
    references = {key.casefold(): value for key, value in references.items()}
    links = list(references.values())
    links.extend(re.findall(r"\[[^\]\n]*\]\(\s*(<[^>\n]+>|[^\s)]+)(?:\s+[\"'][^\n]*?[\"'])?\s*\)", text))
    for label, reference in re.findall(r"\[([^\]\n]+)\]\[([^\]\n]*)\]", text):
        if (reference or label).casefold() not in references:
            errors.append(f"{path.relative_to(root)}: undefined reference [{reference or label}]")
    for link in links:
        parsed = urlsplit(link.strip("<>"))
        if parsed.scheme or parsed.netloc:
            continue
        target = path.parent / unquote(parsed.path) if parsed.path else path
        if not target.exists():
            errors.append(f"{path.relative_to(root)}: broken link {link}")
        elif parsed.fragment:
            if not target.is_file() or target.suffix.lower() not in (".md", ".mdc"):
                errors.append(f"{path.relative_to(root)}: unsupported local fragment {link}")
            elif unquote(parsed.fragment) not in anchors(target):
                errors.append(f"{path.relative_to(root)}: missing anchor {link}")


def inventory(root: Path) -> dict[str, tuple[str, bytes | str]]:
    entries = {}
    for output in OUTPUTS:
        directory = root / output
        for parent in (directory.parent, directory):
            if parent.is_symlink():
                raise ValueError(f"{parent.relative_to(root)}: setup directory must not be a symlink")
        if not directory.exists():
            continue
        if not directory.is_dir():
            raise ValueError(f"{output}: expected setup directory")
        for path in sorted(directory.rglob("*")):
            relative = str(path.relative_to(root))
            if path.is_symlink():
                entries[relative] = ("symlink", os.readlink(path))
            elif path.is_file():
                entries[relative] = ("file", path.read_bytes())
            elif path.is_dir():
                entries[relative] = ("directory", "")
            else:
                raise ValueError(f"{relative}: unsupported setup entry")
    return entries


def validate_toml(root: Path, entries: dict, label: str, errors: list[str]) -> None:
    for path, (kind, contents) in entries.items():
        if not path.startswith(".codex/agents/") or not path.endswith(".toml") or kind != "file":
            continue
        try:
            fields = tomllib.loads(contents.decode("utf-8"))
            if set(fields) != {"name", "description", "developer_instructions"}:
                raise ValueError("expected name, description and developer_instructions")
            if fields["name"] != Path(path).stem or any(not isinstance(v, str) or not v.strip() for v in fields.values()):
                raise ValueError("invalid agent name or empty/non-string TOML field")
            canonical = root / "ai/agents" / f"{Path(path).stem}.md"
            if canonical.is_file():
                metadata, instructions = frontmatter(canonical, "agent")
                if fields["description"] != metadata["description"] or not fields["developer_instructions"].endswith(instructions):
                    raise ValueError("TOML does not preserve canonical description/instructions")
        except (ValueError, UnicodeError) as error:
            errors.append(f"{label} {path}: invalid TOML: {error}")


def validate_generated_manifest(root: Path, generated: dict, errors: list[str]) -> None:
    required = {}
    for pattern, directory, suffix in (
        ("ai/rules/*.mdc", ".claude/rules", ".md"),
        ("ai/agents/*.md", ".claude/agents", ".md"),
        ("ai/skills/*/SKILL.md", ".claude/skills", ""),
        ("ai/skills/*/SKILL.md", ".agents/skills", ""),
    ):
        for source in root.glob(pattern):
            if suffix == "":
                source = source.parent
            destination = Path(directory) / (source.stem + suffix)
            required[str(destination)] = ("symlink", os.path.relpath(source, root / directory))
    for source in root.glob("ai/agents/*.md"):
        required[f".codex/agents/{source.stem}.toml"] = ("file", None)
    for path in sorted(required.keys() | generated.keys()):
        if path not in generated:
            errors.append(f"scripts/ai-setup.sh: missing canonical output {path}")
        elif path not in required:
            errors.append(f"scripts/ai-setup.sh: unexpected generated output {path}")
        else:
            kind, target = required[path]
            if generated[path][0] != kind or (kind == "symlink" and generated[path][1] != target):
                errors.append(f"scripts/ai-setup.sh: wrong canonical mapping for {path}")


def validate_cases(root: Path, guards: set[str], errors: list[str]) -> None:
    path = root / "ai/checks/workflows.json"
    try:
        cases = json.loads(path.read_text())
        if not isinstance(cases, list):
            raise ValueError("expected a list of workflow fixtures")
        ids = set()
        for case in cases:
            if not isinstance(case, dict) or set(case) != {"id", "fixture", "guards", "expected_status", "expected"}:
                raise ValueError("each case needs id, fixture, guards, expected_status and expected")
            name = case["id"]
            if not isinstance(name, str) or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name) or name in ids:
                raise ValueError(f"invalid or duplicate case id: {name}")
            ids.add(name)
            fixture_name = case["fixture"]
            if not isinstance(fixture_name, str) or Path(fixture_name).is_absolute():
                raise ValueError(f"{name}: fixture must be a repository-relative path")
            fixture = (root / fixture_name).resolve()
            if not fixture.is_relative_to(root / "ai/checks") or not fixture.is_file() or not fixture.read_text().strip():
                raise ValueError(f"{name}: missing/unsafe/empty fixture {fixture_name}")
            if not isinstance(case["guards"], list) or not case["guards"] or any(not isinstance(g, str) or g not in guards for g in case["guards"]):
                raise ValueError(f"{name}: unknown or missing guards")
            if case["expected_status"] not in ("PASS", "FAIL", "ACCEPTED") or not isinstance(case["expected"], str) or not case["expected"].strip():
                raise ValueError(f"{name}: missing expected outcome")
        if REQUIRED_CASES - ids:
            raise ValueError(f"missing workflow fixtures: {', '.join(sorted(REQUIRED_CASES - ids))}")
    except (OSError, ValueError) as error:
        errors.append(f"ai/checks/workflows.json: {error}")


def generate(root: Path, temporary: Path) -> dict:
    shutil.copytree(root / "ai", temporary / "ai")
    (temporary / "scripts").mkdir()
    shutil.copy2(root / "scripts/ai-setup.sh", temporary / "scripts/ai-setup.sh")
    return run_setup(temporary)


def run_setup(root: Path) -> dict:
    result = subprocess.run(["bash", "scripts/ai-setup.sh"], cwd=root, capture_output=True, text=True, timeout=60)
    if result.returncode:
        raise ValueError(f"scripts/ai-setup.sh failed ({result.returncode}): {(result.stderr or result.stdout).strip()}")
    return inventory(root)


def check(root: Path) -> list[str]:
    errors: list[str] = []
    documents = [root / "AGENTS.md", root / "CLAUDE.md"]
    for path in sorted((root / "ai").rglob("*")):
        if path.is_symlink():
            errors.append(f"{path.relative_to(root)}: canonical AI input must not be a symlink")
        elif path.suffix in (".md", ".mdc") and path.is_file():
            documents.append(path)
    for directory, suffix in (("ai/rules", ".mdc"), ("ai/agents", ".md")):
        for path in sorted((root / directory).iterdir()):
            if not path.is_file() or path.suffix != suffix:
                errors.append(f"{path.relative_to(root)}: unsupported canonical layout; expected {suffix} file")
    for kind, pattern in (("rule", "ai/rules/*.mdc"), ("agent", "ai/agents/*.md"), ("skill", "ai/skills/*/SKILL.md")):
        paths = sorted(root.glob(pattern))
        if not paths:
            errors.append(f"{pattern}: no canonical {kind} files")
        for path in paths:
            try:
                frontmatter(path, kind)
            except (ValueError, OSError) as error:
                errors.append(f"{path.relative_to(root)}: {error}")
    for directory in sorted((root / "ai/skills").iterdir()):
        if directory.name == ".gitkeep" and directory.is_file():
            continue
        if not directory.is_dir() or not (directory / "SKILL.md").is_file():
            errors.append(f"{directory.relative_to(root)}/SKILL.md: missing skill entry point")
    guards = {path.stem for path in root.glob("ai/agents/*.md")}
    for path in documents:
        try:
            check_links(root, path, errors)
        except (ValueError, OSError) as error:
            errors.append(f"{path.relative_to(root)}: {error}")
    routing = (root / "AGENTS.md").read_text()
    for guard in sorted(set(re.findall(r"\b[a-z]+(?:-[a-z]+)*-guard\b", routing)) - guards):
        errors.append(f"ai/agents/{guard}.md: missing guard referenced by AGENTS.md")
    if (root / "CLAUDE.md").read_text().strip() != "@AGENTS.md":
        errors.append("CLAUDE.md: expected @AGENTS.md entry-point import")
    validate_cases(root, guards, errors)
    if errors:
        return errors
    installed = inventory(root)
    validate_toml(root, installed, "installed", errors)
    with tempfile.TemporaryDirectory(prefix="notifier-ai-check-") as directory:
        temporary = Path(directory)
        expected = generate(root, temporary / "first")
        validate_toml(root, expected, "generated", errors)
        validate_generated_manifest(root, expected, errors)
        if run_setup(temporary / "first") != expected:
            errors.append("scripts/ai-setup.sh: repeated generation is not idempotent")
        if generate(root, temporary / "second") != expected:
            errors.append("scripts/ai-setup.sh: clean generation is not reproducible")
    for path in sorted(expected.keys() | installed.keys()):
        if path not in installed:
            errors.append(f"{path}: missing setup output; run make ai-setup")
        elif path not in expected:
            errors.append(f"{path}: unexpected/obsolete setup output; inspect it and run make ai-setup")
        elif installed[path] != expected[path]:
            errors.append(f"{path}: stale setup output; run make ai-setup")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    root = parser.parse_args().root.resolve()
    try:
        errors = check(root)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        errors = [str(error)]
    if errors:
        for error in errors:
            print(f"FAIL: {error}", file=sys.stderr)
        return 1
    print("PASS: AI links, frontmatter, TOML, setup completeness, freshness and repeat generation")
    print("Workflow fixtures: structure checked only; semantic outcomes require Codex reviewers (ai/checks/README.md).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
