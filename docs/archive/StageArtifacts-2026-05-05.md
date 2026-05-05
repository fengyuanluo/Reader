# 已归档：首轮阶段性执行资产索引

> 状态：已归档 / 索引文档
>
> 归档日期：2026-05-05
>
> 用途：说明 2026-05-03 至 2026-05-05 首轮实现和对齐修复阶段产生的过程性文件如何分层保存，以及它们与当前真相源的关系。

## 当前真相源

- `docs/Requirement.md`
- `docs/Design.md`
- `docs/Tasks.md`
- `docs/LegadoAlignmentReport.md`
- `test/reports/real_source_collection_e2e_report.json`
- `test/reports/real_source_collection_e2e_report.md`

## 已归档阶段性文件

- `docs/archive/task_plan-2026-05-03-to-2026-05-05.md`
  - 角色：首轮执行任务跟踪。
  - 禁止用途：不得作为当前任务状态直接依据。

- `docs/archive/findings-2026-05-03-to-2026-05-05.md`
  - 角色：首轮实现与对齐修复的阶段性 findings 汇总。
  - 禁止用途：不得作为当前通过率、当前 blocker 或当前版本完成度的直接依据。

- `docs/archive/progress-2026-05-03-to-2026-05-05.md`
  - 角色：首轮执行日志与验收轨迹。
  - 禁止用途：不得作为当前实现进度或当前 release 判断的直接依据。

- `docs/archive/GoalCompletionAudit.md`
  - 角色：某一阶段的 objective 完成度审计快照。
  - 禁止用途：不得作为当前真实通过率、当前完成度或当前 release 判断的直接依据。

- `docs/archive/Tasks-2026-05-05-pre-audit-table-rewrite.md`
  - 角色：旧版任务表归档。
  - 禁止用途：不得作为当前兼容缺口、当前优先级或当前 release 修复清单的直接依据。

## 使用规则

- 需要看当前实现边界、当前任务状态或当前 release 结论时，优先看仓库根下 `docs/` 基线文档和最新 `test/reports/` 报告。
- 只有在追溯历史决策、理解为何某条修复进入主线、或审计首轮交付过程时，才回读 `docs/archive/`。
