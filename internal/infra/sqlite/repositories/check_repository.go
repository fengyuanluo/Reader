package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func (r *CheckRepository) FindTask(ctx context.Context, taskID string) (*task.CheckTask, error) {
	checkTask, err := scanCheckTask(r.db.QueryRowContext(ctx, `SELECT task_id, status, total, finished, coalesce(trace_id, ''), created_at, completed_at FROM check_tasks WHERE task_id = ?`, taskID))
	if err != nil {
		return nil, err
	}
	items, err := r.ListItems(ctx, taskID)
	if err != nil {
		return nil, err
	}
	checkTask.Items = items
	return checkTask, nil
}

func (r *CheckRepository) UpdateTaskStatus(ctx context.Context, taskID string, status task.CheckTaskStatus, finished int, completedAt *time.Time) error {
	current, err := r.FindTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := task.ValidateCheckTaskTransition(current.Status, status); err != nil {
		return err
	}
	return execUpdateOne(ctx, r.db, `UPDATE check_tasks SET status = ?, finished = ?, completed_at = ? WHERE task_id = ?`, string(status), finished, formatOptionalTime(completedAt), taskID)
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
	stagesJSON, err := json.Marshal(item.Stages)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
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
  updated_at = excluded.updated_at`,
		item.TaskID, item.SourceID, nullable(item.BookSourceURL), nullable(item.BookSourceName), string(item.Status), nullable(item.FailureCode), nullable(item.FailureClass), nullable(item.Message), item.RespondTime, nullable(item.KeywordUsed), item.TimeoutMS, string(stagesJSON), nullable(item.TraceID), formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
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
