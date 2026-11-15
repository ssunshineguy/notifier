#!/usr/bin/env python3
"""Exercise AI setup checks through the CLI without changing the checked tree."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest


REPOSITORY = Path(__file__).resolve().parents[1]


def fingerprint(root):
    result = {}
    for path in [root, *sorted(root.rglob("*"))]:
        metadata = path.lstat()
        value = (stat.S_IFMT(metadata.st_mode), stat.S_IMODE(metadata.st_mode), metadata.st_mtime_ns)
        if path.is_symlink():
            value += (os.readlink(path),)
        elif path.is_file():
            value += (hashlib.sha256(path.read_bytes()).hexdigest(),)
        result[str(path.relative_to(root))] = value
    return result


class AICheckTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workspace = tempfile.TemporaryDirectory(prefix="notifier-ai-check-tests-")
        cls.addClassCleanup(cls.workspace.cleanup)
        cls.baseline = Path(cls.workspace.name) / "baseline"
        shutil.copytree(
            REPOSITORY,
            cls.baseline,
            symlinks=True,
            ignore=shutil.ignore_patterns(
                ".git", ".claude", ".agents", ".codex", ".venv", "__pycache__",
                ".pytest_cache", "node_modules", "bin", ".env", "*.local.yaml",
                "*.egg-info", "test-results", "playwright-report", "blob-report",
            ),
        )
        result = subprocess.run(
            ["bash", "scripts/ai-setup.sh"], cwd=cls.baseline,
            capture_output=True, text=True, timeout=30,
        )
        if result.returncode:
            raise AssertionError(result.stdout + result.stderr)

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=self.workspace.name, prefix="case-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name) / "checkout"
        shutil.copytree(self.baseline, self.root, symlinks=True)

    def check(self, expected_status=1, diagnostic=None, default_root=False):
        before = fingerprint(self.root)
        command = [sys.executable, "-B", str(self.root / "scripts/ai-check.py")]
        if not default_root:
            command.extend(["--root", str(self.root)])
        result = subprocess.run(command, cwd=self.directory.name, capture_output=True, text=True, timeout=45)
        output = result.stdout + result.stderr
        self.assertEqual(before, fingerprint(self.root), "checker modified its input checkout")
        self.assertFalse(list(self.root.rglob("__pycache__")))
        self.assertEqual(expected_status, result.returncode, output)
        self.assertNotIn("Traceback (most recent call last)", output)
        if diagnostic:
            self.assertRegex(output, diagnostic)
        return output

    def edit(self, relative, old, new):
        path = self.root / relative
        current = path.read_text()
        self.assertIn(old, current)
        path.write_text(current.replace(old, new, 1))

    def append(self, relative, contents):
        with (self.root / relative).open("a") as target:
            target.write(contents)

    def setup(self):
        result = subprocess.run(
            ["bash", "scripts/ai-setup.sh"], cwd=self.root,
            capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_current_setup_is_read_only_and_passes_from_another_directory(self):
        self.check(expected_status=0)
        self.check(expected_status=0, default_root=True)

    def test_missing_canonical_guard_even_after_setup(self):
        (self.root / "ai/agents/go-guard.md").unlink()
        self.setup()
        self.check(diagnostic="go-guard")

    def test_missing_installed_guard(self):
        (self.root / ".codex/agents/scope-guard.toml").unlink()
        self.check(diagnostic="scope-guard")

    def test_stale_valid_toml(self):
        self.edit(".codex/agents/scope-guard.toml", 'description = "Review every', 'description = "Review stale')
        self.check(diagnostic="scope-guard.toml")

    def test_invalid_toml(self):
        self.append(".codex/agents/scope-guard.toml", "invalid = [\n")
        self.check(diagnostic="scope-guard.toml")

    def test_changed_source_requires_setup(self):
        self.append("ai/agents/scope-guard.md", "\nUse supplied approval evidence.\n")
        self.check(diagnostic="scope-guard.toml")

    def test_wrong_symlink(self):
        link = self.root / ".claude/agents/scope-guard.md"
        link.unlink()
        link.symlink_to("../../ai/agents/readme-guard.md")
        self.check(diagnostic="scope-guard.md")

    def test_broken_symlink(self):
        link = self.root / ".claude/rules/tests.md"
        link.unlink()
        link.symlink_to("../../ai/rules/absent.mdc")
        self.check(diagnostic="tests.md")

    def test_obsolete_generated_agent(self):
        shutil.copyfile(
            self.root / ".codex/agents/scope-guard.toml",
            self.root / ".codex/agents/retired-guard.toml",
        )
        self.check(diagnostic="retired-guard.toml")

    def test_obsolete_generated_symlink(self):
        (self.root / ".agents/skills/retired").symlink_to("../../ai/skills/retired")
        self.check(diagnostic="retired")

    def test_frontmatter_duplicate_field(self):
        self.edit("ai/agents/scope-guard.md", "name: scope-guard\n", "name: scope-guard\nname: scope-guard\n")
        self.check(diagnostic="scope-guard.md")

    def test_frontmatter_missing_field(self):
        self.edit("ai/skills/review-changes/SKILL.md", "name: review-changes\n", "")
        self.check(diagnostic="review-changes")

    def test_frontmatter_invalid_boolean(self):
        self.edit("ai/rules/tests.mdc", "alwaysApply: false", "alwaysApply: perhaps")
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_scalar_used_as_list(self):
        self.edit("ai/rules/tests.mdc", "paths:\n", "paths: scalar\n")
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_mapping_used_as_path(self):
        self.edit("ai/rules/tests.mdc", "  - internal/**", "  - nested: mapping")
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_invalid_quoted_path(self):
        self.edit("ai/rules/tests.mdc", "  - internal/**", '  - "unterminated')
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_quoted_boolean(self):
        self.edit("ai/rules/tests.mdc", "alwaysApply: false", 'alwaysApply: "false"')
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_undefined_alias(self):
        self.edit("ai/rules/tests.mdc", "  - internal/**", "  - *missing")
        self.check(diagnostic="tests.mdc")

    def test_frontmatter_non_string_metadata(self):
        path = self.root / "ai/skills/review-changes/SKILL.md"
        original = path.read_text()
        for value in ("true", "null", "42", "1e3", "2026-11-15", "# hidden", "-"):
            with self.subTest(value=value):
                path.write_text(original.replace("description: Run the applicable Notifier guards and verification before handoff or when asked to run guards.", f"description: {value}"))
                self.check(diagnostic="review-changes")

    def test_missing_skill_entry_point(self):
        (self.root / "ai/skills/review-changes/SKILL.md").unlink()
        self.setup()
        self.check(diagnostic="review-changes")

    def test_missing_rule_even_after_setup(self):
        (self.root / "ai/rules/http-api.mdc").unlink()
        self.setup()
        self.check(diagnostic="http-api")

    def test_unsupported_rule_file_cannot_be_silently_omitted(self):
        (self.root / "ai/rules/extra.txt").write_text("A rule that setup would silently skip.\n")
        self.check(diagnostic="extra.txt")

    def test_nested_guard_cannot_be_silently_omitted(self):
        directory = self.root / "ai/agents/nested"
        directory.mkdir()
        shutil.copyfile(self.root / "ai/agents/scope-guard.md", directory / "scope-guard.md")
        self.check(diagnostic="nested")

    def test_broken_markdown_path(self):
        self.append("AGENTS.md", "\n[Absent rule](ai/rules/absent.mdc)\n")
        self.check(diagnostic="absent.mdc")

    def test_broken_markdown_anchor(self):
        self.append("AGENTS.md", "\n[Absent heading](ai/rules/tests.mdc#absent-heading)\n")
        self.check(diagnostic="absent-heading")

    def test_broken_reference_link(self):
        self.append("AGENTS.md", "\nSee [missing reference][missing-rule].\n\n[missing-rule]: ai/rules/absent.mdc\n")
        self.check(diagnostic="absent.mdc")

    def test_undefined_reference_link(self):
        self.append("AGENTS.md", "\nSee [missing reference][unbound-reference].\n")
        self.check(diagnostic="unbound-reference")

    def test_missing_workflow_fixture(self):
        path = self.root / "ai/checks/workflows.json"
        workflows = json.loads(path.read_text())
        workflows[0]["fixture"] = "ai/checks/fixtures/absent.md"
        path.write_text(json.dumps(workflows))
        self.check(diagnostic="absent.md")

    def test_generator_cannot_omit_a_canonical_guard(self):
        self.edit(
            "scripts/ai-setup.sh",
            'agents = sorted(Path("ai/agents").glob("*.md"))',
            'agents = sorted(path for path in Path("ai/agents").glob("*.md") if path.stem != "go-guard")',
        )
        self.setup()
        self.assertFalse((self.root / ".codex/agents/go-guard.toml").exists())
        self.check(diagnostic="go-guard")

    def test_generator_cannot_omit_canonical_guard_instructions(self):
        self.edit("scripts/ai-setup.sh", "+ instructions.strip()", '+ ""')
        self.setup()
        self.check(diagnostic=r"\.toml")

    def test_generator_cannot_omit_a_canonical_rule(self):
        self.edit(
            "scripts/ai-setup.sh",
            'for rule in sorted(Path("ai/rules").glob("*.mdc")):',
            'for rule in sorted(path for path in Path("ai/rules").glob("*.mdc") if path.stem != "tests"):',
        )
        self.setup()
        self.assertFalse((self.root / ".claude/rules/tests.md").exists())
        self.check(diagnostic="tests.md")

    def test_generator_cannot_omit_a_canonical_skill(self):
        self.edit(
            "scripts/ai-setup.sh",
            'if (skill / "SKILL.md").is_file():',
            'if skill.name != "review-changes" and (skill / "SKILL.md").is_file():',
        )
        self.setup()
        self.assertFalse((self.root / ".agents/skills/review-changes").exists())
        self.check(diagnostic="review-changes")

    def test_non_idempotent_setup(self):
        self.append("scripts/ai-setup.sh", "\nprintf x >> .codex/agents/run-count.txt\n")
        self.check(diagnostic=r"(?i)(idempot|repeat|reproduc)")

    def test_independent_generation_is_reproducible(self):
        self.append("scripts/ai-setup.sh", '\nprintf "%s\\n" "$root" > .codex/agents/generation-root.txt\n')
        self.check(diagnostic=r"(?i)(determin|reproduc)")

    def test_setup_failure_is_diagnostic_and_read_only(self):
        self.append("scripts/ai-setup.sh", "\nexit 23\n")
        self.check(diagnostic="ai-setup.sh")

    def test_symlinked_output_parent_preserves_external_target(self):
        outside = Path(self.directory.name) / "outside"
        (self.root / ".codex").rename(outside)
        (self.root / ".codex").symlink_to(outside, target_is_directory=True)
        before = fingerprint(outside)
        self.check(diagnostic=r"\.codex")
        self.assertEqual(before, fingerprint(outside))


if __name__ == "__main__":
    unittest.main()
