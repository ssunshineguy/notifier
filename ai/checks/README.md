# AI setup checks and workflow probes

[AGENTS.md](../../AGENTS.md) and [review-changes](../skills/review-changes/SKILL.md) remain the
workflow authority. These fixtures exercise a few agreed decisions; they are not a second skill
system or general test of AI reviewer correctness. Every fixture is synthetic. Its test evidence,
base IDs, paths, review history and human quotes are exercise inputs, never evidence of real runs
or approval for a change in this repository.

## Automatic checks

Run `make ai-check` from the checkout with Bash and Python 3.11 or newer (the development baseline
is Python 3.14). No third-party packages or network access are required. Run the isolated checker
regression scenarios with `python3 -B scripts/test_ai_check.py`. The checker exits 0 on structural
success and 1 on validation failure; `make ai-check` propagates failure as a nonzero exit status.

The checker validates canonical metadata and links, generated TOML and the exact inventory of
managed output directories. An independent source-to-output map detects omissions by the generator
itself; generated TOML must preserve canonical descriptions and instruction bodies. Extra files
there fail validation, including custom files; the checker never silently removes them. It generates in one temporary checkout twice and in a second fresh
temporary checkout, then compares contents and installed setup without repairing the checked
checkout. Run `make ai-setup` separately to install approved canonical changes, then rerun the checker.

Frontmatter uses a constrained format: flat scalar fields and a block list for rule `paths`, not
arbitrary YAML. Link checks cover Markdown inline/reference links and ATX heading fragments,
including reachable absolute local links. They do not fetch remote URLs or verify every freeform
path written in backticks.

[workflows.json](workflows.json) indexes semantic probes. Automatic validation checks the catalog
structure, required cases, unique IDs, referenced fixture files, guard names and allowed expected
statuses. It does not run an LLM, execute the hypothetical diffs, interpret their SQL, authorize
warnings, prove DND equivalence, or establish that any expected reviewer decision is correct.
A successful structural check must never be reported as semantic workflow PASS.

## Codex reviewer procedure

1. Run the automatic checker. Record the actual checkout/commit, canonical AI diff and result.
2. For each case in `workflows.json`, use a fresh read-only Codex reviewer with the listed canonical
   `ai/agents/<guard>.md` files and their referenced rules. Give it the fixture, the task scope,
   full synthetic diff/base and synthetic approval/test context. These are targeted probes of the
   listed guards, not permission to skip other guards for real code changes. Do not use Claude.
3. Use this delegation prompt, replacing the two placeholders with the selected case and paths:

   ```text
   Work read-only in the supplied repository. Evaluate fixture CASE_ID as a synthetic reviewer
   exercise, not as a request to apply its diff. Read AGENTS.md, the canonical guard instructions
   GUARD_PATHS, their referenced rules and the complete fixture. Treat the fixture's task scope,
   base, diff, prior approvals and test evidence as synthetic inputs only. Report each listed
   guard's actual status and findings with original severity, location, consequence, disposition
   and evidence. Identify stale-rule conflicts instead of conforming to the expected result.
   Do not edit files, run migrations, access external services or contact the user for a fictional
   approval. Explain any expected-result disagreement. Do not claim the fixture proves real code.
   ```

4. Compare the returned findings, not only the headline status, with `expected` and the fixture's
   expected review. Record case ID, guard/rule revision, actual statuses, key finding dispositions,
   pass/fail against the case oracle, and any gap. Keep reviewer output in the task's private report.
   An expected FAIL passes the exercise only when the intended defect is actually identified.
5. Report structural checks and Codex semantic results separately. Real task blockers/warnings still
   follow [guard outcomes](../../AGENTS.md#guard-outcomes); synthetic warning acceptance is not
   actual human acceptance for repository changes.

## Migration integration

The probes follow the merged [migration rules](../rules/migrations.mdc) and
[migration guard](../agents/migration-guard.md): automatic goose migrations contain only short DDL
and bounded read-only schema assertions. Validation belongs to an independently executed production
procedure outside goose and service startup, even when it could use a separate goose transaction.

| Case | Expected guard outcome | Decision checked |
| --- | --- | --- |
| `migration-locks` | FAIL | Automatic validation is forbidden and the DDL transaction also retains its strong lock through the scan. |
| `migration-automatic-validation` | FAIL | A second goose file releases the earlier lock but does not make automatic validation permissible. |
| `migration-separated` | PASS | Short DDL commits before an independent validation runbook with budgets, completion/retry checks and application gates. |

These are targeted Codex reviewer exercises, not a general SQL analyzer. All fixture commands,
observations and version/approval context are explicitly synthetic. The positive fixture supplies
PostgreSQL evidence as exercise input so the reviewer can evaluate the complete runbook contract;
it does not claim those commands ran on this checkout. Without the required evidence, a reviewer
must report the gap rather than infer safety from SQL text or an applied goose version.

After migration rules or these fixtures change, run all three probes with fresh Codex reviewers
using the procedure above. Record actual outcomes separately from the structural checker result.
API e2e verifies the real application stack but does not execute these synthetic migration diffs
or prove their lock behavior. Actual migrations still require the owning change's PostgreSQL
checks of lock lifetime, concurrent access, timeout/retry and fresh/existing database behavior.
