# configs

Shared standard versions of agent configs, such as MCP server configs, hooks, and custom agent definitions.

Agent tool directories (`.kiro/`, `.cursor/`, `.claude/`, ...) are not committed. Configs worth sharing are kept here, and each person copies them to the matching local location.

## Rules

- One subdirectory per tool: `configs/<tool>/`, such as `configs/kiro/` or `configs/cursor/`.
- Inside it, mirror the file layout of the tool's local directory, so the files can be copied as they are.
- No secrets, tokens, or personal paths. Use placeholders such as `${GITHUB_TOKEN}`.
- List where each config is copied to.

## Configs

| config | tool | copy to |
|---|---|---|
