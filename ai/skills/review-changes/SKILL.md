---
name: review-changes
description: Run the applicable Notifier guards and verification before handoff or when asked to run guards.
---

# Review changes

1. Identify the task diff against its intended base, including staged, unstaged, and new files. Use the trigger table in `AGENTS.md`. When asked for all guards, account for each one and say which have no applicable changes.
2. Delegate `migration-guard` before applying changes in `db/migrations/` or `db/init/`, including via Compose. Run the relevant commands and record their results. For Go changes, use `go test ./...`, `go vet ./...`, and `golangci-lint run`. For changed PostgreSQL behavior, run `go test -tags=integration -count=1 ./internal/storage/postgres`. For concurrency and lifecycle changes, add `go test -race ./...`. Use [generate-api](../generate-api/SKILL.md) when checking generated outputs. For Compose changes, run `docker compose config -q` and use [verify-local-api](../verify-local-api/SKILL.md) when runtime behavior changed. Run `git diff --check`.
3. Delegate the remaining applicable guards. Provide the repository root, comparison base, changed files, relevant behavior or approval context, and available test or regeneration evidence. When a named guard is unavailable, ask a read-only reviewer to follow its `ai/agents/<name>.md` instructions. If delegation is unavailable, apply those instructions directly and label the review as such.
4. Resolve findings in scope and recheck the affected parts. Report results and material verification gaps. Calculate the PR percentage only with the [PR coverage methodology](../pr-description/SKILL.md#test-coverage).
