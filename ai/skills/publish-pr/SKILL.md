---
name: publish-pr
description: Prepare or publish a GitHub pull request for Notifier. Use when asked to draft a PR, or when explicitly authorized to commit, push, or open it.
---

# Publish a Notifier PR

1. Inspect the branch, base, remote, and full diff including new files. Check for an existing PR for the branch. Keep unrelated work out of the proposed commit.
2. Reuse verified results for the current diff or follow [review-changes](../review-changes/SKILL.md). Draft the title and body with [pr-description](../pr-description/SKILL.md), including coverage and any agreed migration exception.
3. Show the proposed title and exact body, target repository, and base branch before opening or updating the PR. Wait for approval of the payload and the requested publication unless the user already provided it.
4. Commit when authorized. Stage the scoped files, run `git diff --cached --check`, and use an English action title. Push only when authorized, to the confirmed remote and branch.
5. To open the approved PR, use `gh pr create` with explicit repository, base, head and title, and pass the approved body with `--body-file`. Use the existing PR for the branch if one is already open. Verify the PR URL, base, head commit, and published body.
