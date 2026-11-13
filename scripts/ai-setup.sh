#!/usr/bin/env bash
# Links ai/ into .claude/ so Claude Code picks up the project rules, guards and skills.
set -euo pipefail
shopt -s nullglob

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

link() {
  local target="$1" path="$2"
  mkdir -p "$(dirname "$path")"
  # Relative targets keep the links valid after moving the repository.
  ln -sfn "$(python3 -c 'import os,sys; print(os.path.relpath(sys.argv[1], sys.argv[2]))' "$target" "$(dirname "$path")")" "$path"
  echo "linked ${path} -> ${target}"
}

prune() {
  [ -d "$1" ] || return 0
  find "$1" -maxdepth 1 -type l ! -exec test -e {} \; -print -delete | sed 's/^/removed /'
}

# Claude Code reads rules only from .md files.
for rule in ai/rules/*.mdc; do
  link "$rule" ".claude/rules/$(basename "$rule" .mdc).md"
done

for agent in ai/agents/*.md; do
  link "$agent" ".claude/agents/$(basename "$agent")"
done

for skill in ai/skills/*/; do
  skill="${skill%/}"
  [ -f "$skill/SKILL.md" ] || continue
  link "$skill" ".claude/skills/$(basename "$skill")"
done

prune .claude/rules
prune .claude/agents
prune .claude/skills
