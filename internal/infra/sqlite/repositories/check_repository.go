package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"reader/internal/domain/task"
)

type CheckRepository struct {
	db *sql.DB
}

func NewCheckRepository(db *sql.DB) *CheckRepository {
	return &CheckRepository{db: db}
}

func (r *CheckRepository) CreateTask(ctx context.Context, item *task.CheckTask) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO check_tasks(task_id, status, total, finished, trace_id, created_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.TaskID, string(item.Status), item.Total, item.Finished, nullable(item.TraceID), formatTime(item.CreatedAt), formatOptionalTime(item.CompletedAt))
	return err
}

// FindTask 只读取任务行，不再内联全部条目：条目走 ListItemsPage 按页读取（S03-06），
// 避免大任务每次轮询都全量反序列化 stages_json。
func (r *CheckRepository) FindTask(ctx context.Context, taskID string) (*task.CheckTask, error) {
	return scanCheckTask(r.db.QueryRowContext(ctx, `SELECT task_id, status, total, finished, coalesce(trace_id, ''), created_at, completed_at FROM check_tasks WHERE task_id = ?`, taskID))
}

func (r *CheckRepository) UpdateTaskStatus(ctx context.Context, taskID string, status task.CheckTaskStatus, finished int, completedAt *time.Time) error {
	// 状态机校验折叠进 WHERE status IN (合法前驱)，读-验-写在一条语句内原子完成，
	// 并发的完成与取消只会有一方生效。
	from := task.CheckTaskPredecessors(status)
	args := make([]any, 0, len(from)+4)
	args = append(args, string(status), finished, formatOptionalTime(completedAt), taskID)
	placeholders := make([]string, len(from))
	for i, prev := range from {
		placeholders[i] = "?"
		args = append(args, string(prev))
	}
	if len(from) > 0 {
		res, err := r.db.ExecContext(ctx, `UPDATE check_tasks SET status = ?, finished = ?, completed_at = ? WHERE task_id = ? AND status IN (`+strings.Join(placeholders, ", ")+`)`, args...)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 1 {
			return nil
		}
	}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM check_tasks WHERE task_id = ?`, taskID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return task.ErrInvalidCheckTaskTransition
}

// FailInterruptedTasks 收敛重启前遗留的未完成任务；这些任务的取消句柄只存在于旧进程内存中。
func (r *CheckRepository) FailInterruptedTasks(ctx context.Context, completedAt time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE check_tasks SET status = ?, completed_at = ? WHERE status IN (?, ?)`,
		string(task.CheckTaskFailed), formatOptionalTime(&completedAt),
		string(task.CheckTaskPending), string(task.CheckTaskRunning))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *CheckRepository) UpdateTaskProgress(ctx context.Context, taskID string, finished int) error {
	if finished < 0 {
		finished = 0
	}
	_, err := r.db.ExecContext(ctx, `UPDATE check_tasks
SET finished = CASE WHEN finished < ? THEN ? ELSE finished END
WHERE task_id = ? AND status = ?`, finished, finished, taskID, string(task.CheckTaskRunning))
	return err
}

func (r *CheckRepository) SaveItem(ctx context.Context, item *task.CheckTaskItem) error {
	return r.SaveItems(ctx, []*task.CheckTaskItem{item})
}

// SaveItemWithProgress 在单个事务内保存条目并原子推进 finished 计数（S03-02）：
// 原本的 SaveItem + UpdateTaskProgress 两次写事务合并为一次，finished 只增不减。
func (r *CheckRepository) SaveItemWithProgress(ctx context.Context, item *task.CheckTaskItem, finished int) error {
	stagesJSON, err := json.Marshal(item.Stages)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, upsertCheckTaskItemSQL,
		item.TaskID, item.SourceID, nullable(item.BookSourceURL), nullable(item.BookSourceName), string(item.Status), nullable(item.FailureCode), nullable(item.FailureClass), nullable(item.Message), item.RespondTime, nullable(item.KeywordUsed), item.TimeoutMS, string(stagesJSON), nullable(item.TraceID), formatTime(item.CreatedAt), formatTime(item.UpdatedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE check_tasks
SET finished = CASE WHEN finished < ? THEN ? ELSE finished END
WHERE task_id = ? AND status = ?`, finished, finished, item.TaskID, string(task.CheckTaskRunning)); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveItems 在单个事务内批量保存条目（S03-11）：取消收尾时一次性落库全部排队条目，
// 避免逐条 SaveItem 的数百次串行写。
func (r *CheckRepository) SaveItems(ctx context.Context, items []*task.CheckTaskItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	stmt, err := tx.PrepareContext(ctx, upsertCheckTaskItemSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		stagesJSON, err := json.Marshal(item.Stages)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx,
			item.TaskID, item.SourceID, nullable(item.BookSourceURL), nullable(item.BookSourceName), string(item.Status), nullable(item.FailureCode), nullable(item.FailureClass), nullable(item.Message), item.RespondTime, nullable(item.KeywordUsed), item.TimeoutMS, string(stagesJSON), nullable(item.TraceID), formatTime(item.CreatedAt), formatTime(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upsertCheckTaskItemSQL 是条目表的双键 upsert，供 SaveItem/SaveItemWithProgress 复用。
const upsertCheckTaskItemSQL = `
INSERT INTO check_task_items(task_id, source_id, book_source_url, book_source_name, status, failure_code, failure_class, message, respond_time, keyword_used, timeout_ms, stages_json, trace_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id, source_id) DO UPDATE SET
  book_source_url = excluded.book_source_url,
  book_source_name = excluded.book_source_name,
  status = excluded.status,
  failure_code = excluded.failure_code,
  failure_class = excluded.failure_class,
  message = excluded.message,
  respond_time = excluded.respond_time,
  keyword_used = excluded.keyword_used,
  timeout_ms = excluded.timeout_ms,
  stages_json = excluded.stages_json,
  trace_id = excluded.trace_id,
  updated_at = excluded.updated_at`

// ListItemsPage 按页读取任务条目（S03-06）：任务详情的条目不再一次性全量返回。
func (r *CheckRepository) ListItemsPage(ctx context.Context, taskID string, status *task.CheckTaskItemStatus, page, pageSize int) ([]*task.CheckTaskItem, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	where := ` WHERE task_id = ?`
	args := []any{taskID}
	if status != nil && *status != "" {
		where += ` AND status = ?`
		args = append(args, string(*status))
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM check_task_items`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT task_id, source_id, coalesce(book_source_url, ''), coalesce(book_source_name, ''), status, coalesce(failure_code, ''), coalesce(failure_class, ''), coalesce(message, ''), coalesce(respond_time, 0), coalesce(keyword_used, ''), coalesce(timeout_ms, 0), coalesce(stages_json, '[]'), coalesce(trace_id, ''), created_at, updated_at
FROM check_task_items`+where+` ORDER BY created_at ASC, source_id ASC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []*task.CheckTaskItem{}
	for rows.Next() {
		item, err := scanCheckTaskItem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// HasUnfinishedTasks 判断是否存在 pending/running 的校验任务（S03-04 防重入）。
func (r *CheckRepository) HasUnfinishedTasks(ctx context.Context) (bool, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM check_tasks WHERE status IN (?, ?))`,
		string(task.CheckTaskPending), string(task.CheckTaskRunning)).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}

// findMissingSourceIDsBatchSize 是存在性校验单次查询的 sourceId 上限，避免超长 IN 列表。
const findMissingSourceIDsBatchSize = 500

// FindMissingSourceIDs 分批查询 book_sources，返回不存在的 sourceId（S00-02）：
// Start 只做存在性校验，不再逐个 FindByID 解析全部规则与 RawJSON。
func (r *CheckRepository) FindMissingSourceIDs(ctx context.Context, sourceIDs []string) ([]string, error) {
	missing := []string{}
	for start := 0; start < len(sourceIDs); start += findMissingSourceIDsBatchSize {
		end := start + findMissingSourceIDsBatchSize
		if end > len(sourceIDs) {
			end = len(sourceIDs)
		}
		batch := sourceIDs[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, `SELECT source_id FROM book_sources WHERE source_id IN (`+strings.Join(placeholders, ", ")+`)`, args...)
		if err != nil {
			return nil, err
		}
		found := make(map[string]struct{}, len(batch))
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			found[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for _, id := range batch {
			if _, ok := found[id]; !ok {
				missing = append(missing, id)
			}
		}
	}
	return missing, nil
}

// UpdateSourceDiagnostics 按 sourceId 窄列更新书源诊断字段（S03-02/S03-03）：
// 只读改 respond_time/book_source_group/book_source_comment 三列，
// 不整行重写，也不会覆盖并发修改的其他列。group/comment 的具体变换由调用方给出。
func (r *CheckRepository) UpdateSourceDiagnostics(ctx context.Context, sourceID string, respondTime int64, transform func(group, comment string) (string, string)) error {
	var group, comment string
	if err := r.db.QueryRowContext(ctx, `SELECT coalesce(book_source_group, ''), coalesce(book_source_comment, '') FROM book_sources WHERE source_id = ?`, sourceID).Scan(&group, &comment); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	newGroup, newComment := transform(group, comment)
	_, err := r.db.ExecContext(ctx, `UPDATE book_sources
SET respond_time = ?, book_source_group = ?, book_source_comment = ?, updated_at = CURRENT_TIMESTAMP
WHERE source_id = ?`, respondTime, nullable(newGroup), nullable(newComment), sourceID)
	return err
}

func (r *CheckRepository) ListItems(ctx context.Context, taskID string) ([]*task.CheckTaskItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT task_id, source_id, coalesce(book_source_url, ''), coalesce(book_source_name, ''), status, coalesce(failure_code, ''), coalesce(failure_class, ''), coalesce(message, ''), coalesce(respond_time, 0), coalesce(keyword_used, ''), coalesce(timeout_ms, 0), coalesce(stages_json, '[]'), coalesce(trace_id, ''), created_at, updated_at
FROM check_task_items WHERE task_id = ? ORDER BY created_at ASC, source_id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*task.CheckTaskItem{}
	for rows.Next() {
		item, err := scanCheckTaskItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanCheckTask(s scanner) (*task.CheckTask, error) {
	var item task.CheckTask
	var status string
	var createdAt string
	var completedAt sql.NullString
	err := s.Scan(&item.TaskID, &status, &item.Total, &item.Finished, &item.TraceID, &createdAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	item.Status = task.CheckTaskStatus(status)
	item.CreatedAt, err = parseDBTime(createdAt)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid && completedAt.String != "" {
		t, err := parseDBTime(completedAt.String)
		if err != nil {
			return nil, err
		}
		item.CompletedAt = &t
	}
	return &item, nil
}

func scanCheckTaskItem(s scanner) (*task.CheckTaskItem, error) {
	var item task.CheckTaskItem
	var status string
	var stagesJSON string
	var createdAt, updatedAt string
	if err := s.Scan(&item.TaskID, &item.SourceID, &item.BookSourceURL, &item.BookSourceName, &status, &item.FailureCode, &item.FailureClass, &item.Message, &item.RespondTime, &item.KeywordUsed, &item.TimeoutMS, &stagesJSON, &item.TraceID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	item.Status = task.CheckTaskItemStatus(status)
	if err := json.Unmarshal([]byte(stagesJSON), &item.Stages); err != nil {
		return nil, err
	}
	var err error
	item.CreatedAt, err = parseDBTime(createdAt)
	if err != nil {
		return nil, err
	}
	item.UpdatedAt, err = parseDBTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}
