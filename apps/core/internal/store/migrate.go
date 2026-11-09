package store

import (
	"context"
	"fmt"
)

// migrations only ever grow; each entry upgrades the schema by one version.
var migrations = []string{
	// 1: events, conversation views, effects, usage, auth and settings.
	`
CREATE TABLE events (
  seq       INTEGER PRIMARY KEY AUTOINCREMENT,
  id        TEXT NOT NULL UNIQUE,
  ts        INTEGER NOT NULL,
  type      TEXT NOT NULL,
  actor     TEXT NOT NULL,
  thread_id TEXT,
  task_id   TEXT,
  run_id    TEXT,
  payload   TEXT NOT NULL
);
CREATE INDEX events_thread ON events(thread_id, seq);

CREATE TABLE threads (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  kind       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  archived   INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX threads_main ON threads(kind) WHERE kind = 'main';

CREATE TABLE messages (
  id         TEXT PRIMARY KEY,
  thread_id  TEXT NOT NULL REFERENCES threads(id),
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  error      INTEGER NOT NULL DEFAULT 0,
  reply_to   INTEGER NOT NULL DEFAULT 0,
  event_seq  INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX messages_thread ON messages(thread_id, event_seq);

CREATE TABLE usage (
  event_seq     INTEGER PRIMARY KEY,
  ts            INTEGER NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  role          TEXT NOT NULL,
  input_tokens  INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  cached_tokens INTEGER NOT NULL,
  thread_id     TEXT,
  task_id       TEXT,
  run_id        TEXT
);

CREATE TABLE effects (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  key        TEXT NOT NULL,
  payload    BLOB,
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  due_at     INTEGER NOT NULL,
  UNIQUE(kind, key)
);
CREATE INDEX effects_due ON effects(due_at);

CREATE TABLE credentials (
  id         TEXT PRIMARY KEY,
  data       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  csrf       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`,
	// 2: memories, and a kind for messages (notices, silent turns).
	`
ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT '';

CREATE TABLE memories (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  content      TEXT NOT NULL,
  source_event INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX memories_kind ON memories(kind, updated_at);
`,
	// 3: machines (nodes), agent runs and the approvals they ask for.
	`
CREATE TABLE nodes (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  os         TEXT NOT NULL,
  arch       TEXT NOT NULL,
  hostname   TEXT NOT NULL DEFAULT '',
  version    TEXT NOT NULL DEFAULT '',
  agents     TEXT NOT NULL DEFAULT '[]',
  online     INTEGER NOT NULL DEFAULT 0,
  last_seen  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  revoked    INTEGER NOT NULL DEFAULT 0
);

-- Device tokens are secrets, not facts: they never enter the event log.
CREATE TABLE node_tokens (
  node_id    TEXT PRIMARY KEY REFERENCES nodes(id),
  token_hash TEXT NOT NULL UNIQUE
);

CREATE TABLE runs (
  id         TEXT PRIMARY KEY,
  thread_id  TEXT NOT NULL DEFAULT '',
  node_id    TEXT NOT NULL,
  agent      TEXT NOT NULL,
  kind       TEXT NOT NULL,
  repo_path  TEXT NOT NULL DEFAULT '',
  prompt     TEXT NOT NULL,
  status     TEXT NOT NULL,
  workdir    TEXT NOT NULL DEFAULT '',
  branch     TEXT NOT NULL DEFAULT '',
  summary    TEXT NOT NULL DEFAULT '',
  diffstat   TEXT NOT NULL DEFAULT '',
  error      TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  started_at INTEGER NOT NULL DEFAULT 0,
  ended_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX runs_node ON runs(node_id, status);
CREATE INDEX runs_thread ON runs(thread_id, created_at);

CREATE TABLE approvals (
  id          TEXT PRIMARY KEY,
  run_id      TEXT NOT NULL REFERENCES runs(id),
  thread_id   TEXT NOT NULL DEFAULT '',
  title       TEXT NOT NULL,
  tool_kind   TEXT NOT NULL DEFAULT '',
  detail      TEXT NOT NULL DEFAULT '',
  options     TEXT NOT NULL DEFAULT '[]',
  status      TEXT NOT NULL,
  option_id   TEXT NOT NULL DEFAULT '',
  decided_by  TEXT NOT NULL DEFAULT '',
  note        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  decided_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX approvals_status ON approvals(status, created_at);
`,
	// 4: tasks, projects and routing, task runs, approval policy, push subscriptions.
	`
CREATE TABLE projects (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  repo_url      TEXT NOT NULL DEFAULT '',
  default_mode  TEXT NOT NULL DEFAULT '',
  default_agent TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE project_nodes (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  node_id    TEXT NOT NULL,
  path       TEXT NOT NULL,
  priority   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, node_id)
);

CREATE TABLE tasks (
  id               TEXT PRIMARY KEY,
  parent_id        TEXT NOT NULL DEFAULT '',
  origin_thread_id TEXT NOT NULL DEFAULT '',
  kind             TEXT NOT NULL,
  title            TEXT NOT NULL,
  detail           TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL,
  status_reason    TEXT NOT NULL DEFAULT '',
  priority         INTEGER NOT NULL DEFAULT 0,
  project_id       TEXT NOT NULL DEFAULT '',
  agent_pref       TEXT NOT NULL DEFAULT '',
  node_pref        TEXT NOT NULL DEFAULT '',
  permission_mode  TEXT NOT NULL DEFAULT '',
  branch           TEXT NOT NULL DEFAULT '',
  base_branch      TEXT NOT NULL DEFAULT '',
  pr_url           TEXT NOT NULL DEFAULT '',
  review_note      TEXT NOT NULL DEFAULT '',
  next             TEXT NOT NULL DEFAULT '',
  dismissed        INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE INDEX tasks_status ON tasks(status, priority, created_at);
CREATE INDEX tasks_parent ON tasks(parent_id);

CREATE TABLE task_deps (
  task_id    TEXT NOT NULL,
  depends_on TEXT NOT NULL,
  PRIMARY KEY (task_id, depends_on)
);

ALTER TABLE nodes ADD COLUMN concurrency INTEGER NOT NULL DEFAULT 2;

ALTER TABLE runs ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN purpose TEXT NOT NULL DEFAULT 'work';
ALTER TABLE runs ADD COLUMN permission_mode TEXT NOT NULL DEFAULT 'default';
ALTER TABLE runs ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN pr_url TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN conflict INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN force INTEGER NOT NULL DEFAULT 0;
CREATE INDEX runs_task ON runs(task_id, created_at);

ALTER TABLE approvals ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN category TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN review TEXT NOT NULL DEFAULT 'user';
ALTER TABLE approvals ADD COLUMN escalation TEXT NOT NULL DEFAULT '';

-- Push subscriptions hold the browser's keys: local state, not events.
CREATE TABLE push_subscriptions (
  endpoint   TEXT PRIMARY KEY,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
`,
	// 5: schedules and trigger state, inspector findings, paused and
	// archived runs, follow-up messages to runs, recovery notes.
	`
CREATE TABLE schedules (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  cron       TEXT NOT NULL,
  tz         TEXT NOT NULL DEFAULT '',
  prompt     TEXT NOT NULL,
  thread_id  TEXT NOT NULL DEFAULT '',
  enabled    INTEGER NOT NULL DEFAULT 1,
  last_run   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- When each trigger key last fired, so nothing is followed up twice.
CREATE TABLE trigger_state (
  key        TEXT PRIMARY KEY,
  last_fired INTEGER NOT NULL
);

CREATE TABLE findings (
  id         TEXT PRIMARY KEY,
  run_id     TEXT NOT NULL DEFAULT '',
  task_id    TEXT NOT NULL DEFAULT '',
  rule       TEXT NOT NULL,
  severity   TEXT NOT NULL,
  action     TEXT NOT NULL,
  reason     TEXT NOT NULL,
  done       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX findings_run ON findings(run_id, rule);

CREATE TABLE run_messages (
  id         TEXT PRIMARY KEY,
  run_id     TEXT NOT NULL,
  text       TEXT NOT NULL,
  delivered  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX run_messages_run ON run_messages(run_id, created_at);

ALTER TABLE runs ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN recovery_note TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN last_event INTEGER NOT NULL DEFAULT 0;

CREATE INDEX usage_ts ON usage(ts);
`,
	// 6: M5 workbench: personas, repositories found in workspaces, machine
	// roles, project layout and deploy target, message context.
	`
CREATE TABLE personas (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  glyph        TEXT NOT NULL DEFAULT '',
  color        TEXT NOT NULL DEFAULT '',
  agent        TEXT NOT NULL DEFAULT '',
  options      TEXT NOT NULL DEFAULT '{}',
  brief        TEXT NOT NULL DEFAULT '',
  default_mode TEXT NOT NULL DEFAULT '',
  kinds        TEXT NOT NULL DEFAULT '[]',
  builtin      INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE repo_scans (
  node_id    TEXT NOT NULL,
  path       TEXT NOT NULL,
  remote     TEXT NOT NULL DEFAULT '',
  remote_key TEXT NOT NULL DEFAULT '',
  branch     TEXT NOT NULL DEFAULT '',
  dirty      INTEGER NOT NULL DEFAULT 0,
  wokspace   INTEGER NOT NULL DEFAULT 0,
  seen_at    INTEGER NOT NULL,
  PRIMARY KEY (node_id, path)
);
CREATE INDEX repo_scans_key ON repo_scans(remote_key);

ALTER TABLE projects ADD COLUMN remote_key TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN default_persona TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN layout TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN deploy TEXT NOT NULL DEFAULT '';
ALTER TABLE project_nodes ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';

ALTER TABLE tasks ADD COLUMN persona_id TEXT NOT NULL DEFAULT '';

ALTER TABLE nodes ADD COLUMN role TEXT NOT NULL DEFAULT 'dev';
ALTER TABLE nodes ADD COLUMN workspaces TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN wok TEXT NOT NULL DEFAULT '';

ALTER TABLE runs ADD COLUMN options TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN persona TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN setup TEXT NOT NULL DEFAULT '';

ALTER TABLE messages ADD COLUMN context TEXT NOT NULL DEFAULT '';
`,
	// 7: archived projects (0 means active).
	`
ALTER TABLE projects ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0;
`,
	// 8: project chats (threads scoped to one project).
	`
ALTER TABLE threads ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
CREATE INDEX threads_project ON threads(project_id, updated_at);
`,
	// 9: where each checkout stands against its upstream (multi-machine-sync),
	// and the model providers configured on the page with their API keys.
	`
ALTER TABLE repo_scans ADD COLUMN git TEXT NOT NULL DEFAULT '';
CREATE TABLE model_providers (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  base_url    TEXT NOT NULL DEFAULT '',
  api_key_env TEXT NOT NULL DEFAULT '',
  models      TEXT NOT NULL DEFAULT '[]',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE model_keys (
  provider_id TEXT PRIMARY KEY,
  api_key     TEXT NOT NULL
);
`,
	// 10: context segments of a thread (long-main-thread).
	`
CREATE TABLE thread_segments (
  thread_id    TEXT NOT NULL,
  segment      INTEGER NOT NULL,
  covers_up_to INTEGER NOT NULL,
  summary      TEXT NOT NULL,
  profile      TEXT NOT NULL DEFAULT '[]',
  reason       TEXT NOT NULL DEFAULT '',
  model        TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL DEFAULT 0,
  event_seq    INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (thread_id, segment)
);
`,
	// 11: files sent with a message.
	`ALTER TABLE messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '';`,
	// 12: MCP servers the supervisor can use (decision 0021).
	`
CREATE TABLE mcp_servers (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  transport      TEXT NOT NULL,
  command        TEXT NOT NULL DEFAULT '',
  args           TEXT NOT NULL DEFAULT '[]',
  url            TEXT NOT NULL DEFAULT '',
  env            TEXT NOT NULL DEFAULT '{}',
  headers        TEXT NOT NULL DEFAULT '{}',
  disabled_tools TEXT NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  pending        INTEGER NOT NULL DEFAULT 0,
  source         TEXT NOT NULL DEFAULT 'user',
  note           TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
`,
	// 13: flexible tasks (decision 0022): where a task works, how it is
	// reviewed, where its branch starts and what became of it; git runs,
	// review runs and project consultants; deployed commits; who wrote a
	// consult message.
	`
ALTER TABLE tasks ADD COLUMN place TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN review TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN on_fail TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN base_from TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN base_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN git_state TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN handoff TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN deploy_commit TEXT NOT NULL DEFAULT '';
UPDATE tasks SET place = 'worktree', review = 'model', on_fail = 'rework', base_from = 'remote' WHERE kind = 'code';
UPDATE tasks SET place = 'scratch', review = 'model', on_fail = 'rework' WHERE kind = 'general';
UPDATE tasks SET place = 'in_place', review = 'none', on_fail = 'ask' WHERE kind = 'ops';
UPDATE tasks SET git_state = 'pr' WHERE pr_url != '';
UPDATE tasks SET git_state = 'unmerged' WHERE kind = 'code' AND pr_url = '' AND branch != '' AND status = 'done';

ALTER TABLE runs ADD COLUMN base_from TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN base_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN git TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN git_result TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN session TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN resume_session TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN consult_closed INTEGER NOT NULL DEFAULT 0;
CREATE INDEX runs_consult ON runs(project_id, purpose, created_at);

ALTER TABLE projects ADD COLUMN deployed_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE repo_scans ADD COLUMN heads TEXT NOT NULL DEFAULT '';

ALTER TABLE messages ADD COLUMN source TEXT NOT NULL DEFAULT '';
`,
	// End-to-end review fixes: how a run ended beyond its status.
	`
ALTER TABLE runs ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN error_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN refused TEXT NOT NULL DEFAULT '';
ALTER TABLE repo_scans ADD COLUMN head_times TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN last_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN last_node TEXT NOT NULL DEFAULT '';
UPDATE tasks SET
	last_agent = COALESCE((SELECT r.agent FROM runs r WHERE r.task_id = tasks.id AND r.purpose IN ('work', 'rebase') ORDER BY r.created_at DESC LIMIT 1), ''),
	last_node = COALESCE((SELECT r.node_id FROM runs r WHERE r.task_id = tasks.id AND r.purpose IN ('work', 'rebase') ORDER BY r.created_at DESC LIMIT 1), '');
`,
	// Decision 0024: a task's own session settings (model, effort).
	`ALTER TABLE tasks ADD COLUMN options TEXT NOT NULL DEFAULT '';
`,
	// Decision 0025: the short prompt of a run that goes on in the task's
	// earlier agent session.
	`ALTER TABLE runs ADD COLUMN continue_prompt TEXT NOT NULL DEFAULT '';
`,
	// The supervisor's own tool calls, shown under its messages (JSON list
	// of event.ToolNote).
	`ALTER TABLE messages ADD COLUMN tools TEXT NOT NULL DEFAULT '';
`,
	// Approval evidence is separate from the display detail and is replayable.
	`ALTER TABLE approvals ADD COLUMN facts TEXT NOT NULL DEFAULT '';
 ALTER TABLE approvals ADD COLUMN evidence TEXT NOT NULL DEFAULT '';
 CREATE TABLE approval_checks (id TEXT PRIMARY KEY, approval_id TEXT NOT NULL, run_id TEXT NOT NULL, tool TEXT NOT NULL, args_digest TEXT NOT NULL, summary TEXT NOT NULL, result TEXT NOT NULL, created_at INTEGER NOT NULL);
 CREATE INDEX approval_checks_run ON approval_checks(run_id);
`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if err != nil {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (0)`); err != nil {
			return err
		}
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this build (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
