---
name: pr-description
description: Draft or revise a GitHub pull request title and description for the Notifier repository using its agreed PR structure.
---

# Notifier pull request descriptions

Start the English title with an action verb and name the outcome. Write the description in English with these sections in order:

1. **Why** explains the problem and the intended outcome in a short paragraph.
2. **What's inside** groups changes by purpose and calls out effects on API clients, operators, or existing data.
3. **Checks** lists commands actually run and the results of key scenarios. Report failed checks, unverified scenarios that matter to the change, and agreed exceptions explicitly.
4. **Test coverage** reports total Go statement coverage using the command below.
5. **Manual steps before rollout** gives exact commands in execution order. Record rollout assumptions and exceptions with their consequences. Write `None` when no manual steps are required.

## Test coverage

Report the percentage under the separate `## Test coverage` heading. Use the `total:` value from `go tool cover`, which is the percentage of statements executed at least once across the selected packages. Include packages with no tests. Exclude the generated ogen package at `internal/server/codegen`. Use the default Go test suite across the whole repository. Report manual smoke checks separately. Use this same command on both branches when comparing coverage.

Run from the repository root with the Go version specified by `go.mod`:

```bash
(
  set -euo pipefail
  export GOFLAGS= GOWORK=off
  coverage_dir=$(mktemp -d)
  trap 'rm -rf "$coverage_dir"' EXIT
  module=$(go list -m)
  packages=$(go list ./... | awk -v generated="$module/internal/server/codegen" '$0 != generated' | paste -sd , -)
  go test -count=1 -covermode=set -coverpkg="$packages" -coverprofile="$coverage_dir/coverage.out" ./...
  go tool cover -func="$coverage_dir/coverage.out" | awk '$1 == "total:" {print $NF; found = 1} END {if (!found) exit 1}'
)
```

Put a colored circle next to the measured percentage in the GitHub PR:

- 🔴 Below 60% (below acceptable)
- 🟡 At least 60% and below 75% (acceptable)
- 🟢 At least 75% and below 90% (good)
- 🔵 90% or higher (excellent)

For example, `🟢 Go statement coverage: **78.2%** (excluding generated ogen code).` If coverage could not be measured, report the reason in this section.

See `ai/rules/migrations.mdc` for manual index builds on existing tables.
