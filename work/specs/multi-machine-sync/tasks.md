# 任务

- [x] 1. `workspace.Prepare` 先 fetch，从 `origin/<默认分支>` 开 worktree；fetch 失败退回本地并在 Run 里说明；补 `task_e2e_test` 的“机器落后远端”用例
- [x] 2. 扫描报告位置状态（branch、ahead / behind、dirty、fetched_at）；`proto.Repo`、Hub 投影和迁移
- [x] 3. daemon 后台定时 fetch 项目位置（`auto_fetch`）
- [x] 4. 工作台和项目弹窗显示位置状态；主管环境说明加上
- [x] 5. `repo.sync`、`repo.stash_wip` 两个请求，Hub 接口和页面菜单
- [x] 6. 任务记录改动的文件，并行任务重叠时提醒主管
- [x] 7. 更新 `docs/api/core-hub-node.md`、`core-hub-http.md`、`docs/architecture/workbench.md`
