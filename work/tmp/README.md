# tmp

Scratch space. Nothing here is committed except this file.

## Rules

- Each task uses its own subdirectory, `work/tmp/<task-slug>/`, for drafts, one-off scripts, debug output, and experiments.
- When the task ends, move anything valuable to its proper place (scripts to `work/scripts/`, conclusions to a spec or `docs/`), then delete the subdirectory.
- Git worktrees you create go in `work/tmp/worktrees/<branch>/`. Delete them after merging or abandoning.
- When unsure where something goes, put it here, never at the root or in another directory.
