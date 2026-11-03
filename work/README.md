# work

Everything that is not in `apps/`, `build/`, or `docs/`.

| directory | purpose | committed |
|---|---|---|
| `agent/` | agent material not tied to one agent tool | yes |
| `specs/` | requirements, designs, tasks | yes |
| `scripts/` | reusable tool scripts | yes |
| `tests/` | tests across apps | yes |
| `assets/` | shared assets and asset sources | yes |
| `infra/` | deployment and infrastructure across apps | yes |
| `tmp/` | scratch space | no |
| `cache/` | intermediate output | no |
| `logs/` | logs | no |

These directories are fixed: never add, delete, or rename them. Each one's rules are in its `README.md`.

When unsure where something goes, put it in `work/tmp/<task-slug>/`.
