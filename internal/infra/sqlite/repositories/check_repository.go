package repositories

import (
	"context"
	"database/sql"
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

func (r *CheckRepository) SaveItem(ctx context.Context, item *task.CheckTaskItem) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO check_task_items(task_id, source_id, book_source_url, book_source_name, status, failure_code, failure_class, message, respond_time, trace_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id, source_id) DO UPDATE SET
  book_source_url = excluded.book_source_url,
  book_source_name = excluded.book_source_name,
  status = excluded.status,
  failure_code = excluded.failure_code,
  failure_class = excluded.failure_class,
  message = excluded.message,
  respond_time = excluded.respond_time,
  trace_id = excluded.trace_id,
  updated_at = excluded.updated_at`,
		item.TaskID, item.SourceID, nullable(item.BookSourceURL), nullable(item.BookSourceName), string(item.Status), nullable(item.FailureCode), nullable(item.FailureClass), nullable(item.Message), item.RespondTime, nullable(item.TraceID), formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
	return err
}

func (r *CheckRepository) ListItems(ctx context.Context, taskID string) ([]*task.CheckTaskItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT task_id, source_id, coalesce(book_source_url, ''), coalesce(book_source_name, ''), status, coalesce(failure_code, ''), coalesce(failure_class, ''), coalesce(message, ''), coalesce(respond_time, 0), coalesce(trace_id, ''), created_at, updated_at
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
	var createdAt, updatedAt string
	if err := s.Scan(&item.TaskID, &item.SourceID, &item.BookSourceURL, &item.BookSourceName, &status, &item.FailureCode, &item.FailureClass, &item.Message, &item.RespondTime, &item.TraceID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	item.Status = task.CheckTaskItemStatus(status)
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
