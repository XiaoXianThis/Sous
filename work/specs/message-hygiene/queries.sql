-- message-hygiene：hub 数据库只读统计清单（见 plan.md 第 1.4 节）
--
-- 用法（在 hub 服务器上，建议先复制一份库再查，避免和运行中的 hub 抢锁）：
--   sqlite3 -readonly /opt/sous/data/hub.db < queries.sql
-- 全部是 SELECT，没有任何写操作。表名、列名按 apps/core/internal/store/migrate.go 核对过。
-- 时间戳单位：毫秒（event.New 用 time.Now().UnixMilli()）。
-- 主线程：threads.kind = 'main'。

PRAGMA query_only = 1;
.headers on
.mode column

-- Q1 话题总量、按状态分
SELECT status, COUNT(*) AS topics FROM topics GROUP BY status;

-- Q2 主线话题总量、未关闭数
SELECT COUNT(*) AS main_topics,
       SUM(t.status = 'open') AS main_open
FROM topics t JOIN threads th ON th.id = t.thread_id
WHERE th.kind = 'main';

-- Q3 话题按“来源”分类：看开话题的那条消息（话题 id = 第一条消息 id）。
--    trigger 再按事件里的 kind（cron / followup / task）和 key 前缀细分：
--    followup 的 key 前缀 question/task/run/approval；task 的 key 前缀 report/dep/machine/fallback/ask；
--    overlap:* 是并行任务改到相同文件。
SELECT
  CASE
    WHEN m.role = 'user' AND m.kind = '' THEN 'user'
    WHEN m.kind = 'trigger' THEN 'trigger:' || IFNULL(json_extract(e.payload, '$.kind'), '?') || ':' ||
         IFNULL(substr(json_extract(e.payload, '$.key'), 1, instr(json_extract(e.payload, '$.key') || ':', ':') - 1), '?')
    WHEN m.kind = 'report' AND m.task_id <> '' THEN 'report:task'
    WHEN m.kind = 'report' THEN 'report:no-task'
    ELSE m.role || ':' || m.kind
  END AS source,
  COUNT(*) AS topics,
  SUM(t.status = 'open') AS still_open
FROM topics t
LEFT JOIN messages m ON m.id = t.id
LEFT JOIN events e ON e.seq = m.event_seq
GROUP BY source
ORDER BY topics DESC;

-- Q4 最老的未关闭话题（年龄按小时），前 10 个
SELECT t.id, t.title,
       ROUND((strftime('%s','now') * 1000 - t.created_at) / 3600000.0, 1) AS age_h,
       ROUND((strftime('%s','now') * 1000 - t.updated_at) / 3600000.0, 1) AS idle_h
FROM topics t WHERE t.status = 'open'
ORDER BY t.created_at LIMIT 10;

-- Q5 未关闭话题里，超过 24 小时没有新消息的有多少
SELECT COUNT(*) AS open_idle_24h
FROM topics WHERE status = 'open'
  AND updated_at < strftime('%s','now') * 1000 - 24 * 3600000;

-- Q6 单个话题消息数：最多的 10 个，和整体分布
SELECT topic_id, COUNT(*) AS msgs FROM messages WHERE topic_id <> ''
GROUP BY topic_id ORDER BY msgs DESC LIMIT 10;
SELECT MAX(n) AS max_msgs, ROUND(AVG(n), 1) AS avg_msgs, COUNT(*) AS topics_with_msgs
FROM (SELECT COUNT(*) AS n FROM messages WHERE topic_id <> '' GROUP BY topic_id);

-- Q7 话题前没有话题的“更早的对话”消息数
SELECT thread_id, COUNT(*) AS legacy_msgs FROM messages WHERE topic_id = '' GROUP BY thread_id;

-- Q8 未关闭话题里，没有绑定任何任务的（系统自动开的大多在这里）
SELECT COUNT(*) AS open_without_tasks
FROM topics t WHERE t.status = 'open'
  AND NOT EXISTS (SELECT 1 FROM tasks k WHERE k.topic_id = t.id);

-- Q9 未关闭话题里，只有“触发 + 主管静默”的（stay_silent 后话题永远不关）
SELECT COUNT(*) AS open_trigger_silent_only
FROM topics t
JOIN messages first ON first.id = t.id AND first.kind = 'trigger'
WHERE t.status = 'open'
  AND NOT EXISTS (SELECT 1 FROM messages x WHERE x.topic_id = t.id
                  AND x.id <> t.id AND NOT (x.kind = 'silent' OR x.kind = 'trigger'));

-- Q10 同名的未关闭话题（“进行中”里看起来重复的）
SELECT title, COUNT(*) AS n FROM topics WHERE status = 'open'
GROUP BY title HAVING n > 1 ORDER BY n DESC LIMIT 20;

-- Q11 一个任务的汇报落在多个话题里（postThread 的 bug 会产生这种情况），列出任务和话题
SELECT task_id, COUNT(DISTINCT topic_id) AS topics, GROUP_CONCAT(DISTINCT topic_id) AS topic_ids
FROM messages WHERE task_id <> '' AND topic_id <> '' AND kind = 'report'
GROUP BY task_id HAVING topics > 1 ORDER BY topics DESC LIMIT 20;

-- Q12 被 git 操作汇报“抢走”了话题的任务：tasks.topic_id 指向的话题第一条消息就是 git 汇报
--     （GitReport 文本以 “提交/合并/推送/开 PR/撤回/删除分支/撤销改动/健康检查” 等开头，按 task.updated 事件找更稳）
SELECT e.task_id, COUNT(*) AS topic_moves
FROM events e
WHERE e.type = 'task.updated' AND json_extract(e.payload, '$.topic_id') IS NOT NULL
GROUP BY e.task_id HAVING topic_moves > 1 ORDER BY topic_moves DESC LIMIT 20;

-- Q13 “需要你处理”里的任务，按入选原因分（与 task/routes.go 的 inbox 条件一致）
SELECT
  CASE
    WHEN k.status = 'done' AND k.pr_url <> '' THEN 'done+PR（已完成却仍在列）'
    WHEN k.status IN ('blocked','failed') THEN k.status
    WHEN k.status = 'queued' THEN 'queued:等待机器'
    WHEN k.status = 'review' THEN 'review:等你验收'
    ELSE k.status || ':已暂停'
  END AS why,
  COUNT(*) AS tasks,
  ROUND(MAX(strftime('%s','now') * 1000 - k.updated_at) / 86400000.0, 1) AS oldest_days
FROM tasks k
LEFT JOIN projects p ON p.id = k.project_id
WHERE IFNULL(p.archived_at, 0) = 0 AND (
     (k.dismissed = 0 AND k.status IN ('blocked','failed'))
  OR (k.dismissed = 0 AND k.status = 'queued' AND k.status_reason LIKE '等待机器%')
  OR (k.dismissed = 0 AND k.status = 'review' AND k.status_reason LIKE '%等你验收%')
  OR (k.status IN ('running','waiting_approval') AND k.status_reason LIKE '已暂停%')
  OR (k.status = 'done' AND k.pr_url <> '' AND k.dismissed = 0))
GROUP BY why;

-- Q14 等用户审批的审批单
SELECT COUNT(*) AS pending_user_approvals,
       ROUND(MAX(strftime('%s','now') * 1000 - created_at) / 3600000.0, 1) AS oldest_h
FROM approvals WHERE status = 'pending' AND review = 'user';

-- Q15 话题“要你处理”分组（前端 topicGroup：话题里任一任务是 attention）里，
--     话题本身已关闭、或任务已被标记已处理（dismissed）的——这些前端仍算“要你处理”
SELECT t.status AS topic_status, k.dismissed, k.status AS task_status, COUNT(DISTINCT t.id) AS topics
FROM topics t JOIN tasks k ON k.topic_id = t.id
WHERE k.status IN ('blocked','failed','waiting_approval')
   OR (k.status = 'queued' AND k.status_reason LIKE '等待机器%')
   OR (k.status = 'running' AND k.status_reason LIKE '已暂停%')
   OR (k.status = 'review' AND k.status_reason LIKE '%等你验收%')
GROUP BY t.status, k.dismissed, k.status;

-- Q16 最近 7 天每天新开的话题数，按来源（看增长速度）
SELECT date(t.created_at / 1000, 'unixepoch', 'localtime') AS day,
       SUM(m.role = 'user' AND m.kind = '') AS by_user,
       SUM(m.kind = 'trigger') AS by_trigger,
       SUM(m.kind = 'report') AS by_report,
       SUM(m.kind IN ('notice','inspector')) AS by_notice,
       COUNT(*) AS total
FROM topics t LEFT JOIN messages m ON m.id = t.id
WHERE t.created_at > strftime('%s','now') * 1000 - 7 * 86400000
GROUP BY day ORDER BY day;

-- Q17 表规模参照
SELECT 'events' AS tbl, COUNT(*) AS n FROM events
UNION ALL SELECT 'messages', COUNT(*) FROM messages
UNION ALL SELECT 'topics', COUNT(*) FROM topics
UNION ALL SELECT 'tasks', COUNT(*) FROM tasks
UNION ALL SELECT 'runs', COUNT(*) FROM runs
UNION ALL SELECT 'trigger_state', COUNT(*) FROM trigger_state;
