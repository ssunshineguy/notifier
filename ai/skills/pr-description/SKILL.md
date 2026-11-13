---
name: pr-description
description: Draft or revise a GitHub pull request title and description for the Notifier repository using its agreed PR structure.
---

# Notifier pull request descriptions

Start the English title with an action verb and name the outcome. Write the description in English with these sections in order:

1. **Why** explains the problem and the intended outcome in a short paragraph.
2. **What's inside** groups changes by purpose and calls out effects on API clients, operators, or existing data.
3. **Checks** lists commands actually run and the results of key scenarios. Report failed checks, unverified scenarios that matter to the change, and agreed exceptions explicitly.
4. **Manual steps before rollout** gives exact commands in execution order. Record rollout assumptions and exceptions with their consequences. Write `None` when no manual steps are required.

See `ai/rules/migrations.mdc` for manual index builds on existing tables.
