package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"reader/internal/domain/task"
)

type DebugRepository struct {
	db *sql.DB
}

func NewDebugRepository(db *sql.DB) *DebugRepository {
	return &DebugRepository{db: db}
}

func (r *DebugRepository) CreateSession(ctx context.Context, session *task.DebugSession) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO debug_sessions(session_id, source_id, mode, status, keyword, url, trace_id, created_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.SessionID, session.SourceID, string(session.Mode), string(session.Status), nullable(session.Keyword), nullable(session.URL), nullable(session.TraceID), formatTime(session.CreatedAt), formatOptionalTime(session.CompletedAt))
	return err
}

func (r *DebugRepository) FindSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	return scanDebugSession(r.db.QueryRowContext(ctx, `SELECT session_id, source_id, mode, status, coalesce(keyword, ''), coalesce(url, ''), coalesce(trace_id, ''), created_at, completed_at FROM debug_sessions WHERE session_id = ?`, sessionID))
}

// UpdateSessionStatus 只在会话未进入终态时写入终态，避免取消与自然结束并发时互相覆盖。
func (r *DebugRepository) UpdateSessionStatus(ctx context.Context, sessionID string, status task.DebugSessionStatus, completedAt *time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE debug_sessions SET status = ?, completed_at = ? WHERE session_id = ? AND status NOT IN (?, ?, ?)`,
		string(status), formatOptionalTime(completedAt), sessionID,
		string(task.DebugSessionSucceeded), string(task.DebugSessionFailed), string(task.DebugSessionCancelled))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		return true, nil
	}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM debug_sessions WHERE session_id = ?`, sessionID).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, ErrNotFound
	}
	return false, nil
}

// FailInterruptedSessions 收敛重启前遗留的未结束会话；这些会话的取消句柄只存在于旧进程内存中。
func (r *DebugRepository) FailInterruptedSessions(ctx context.Context, completedAt time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE debug_sessions SET status = ?, completed_at = ? WHERE status IN (?, ?)`,
		string(task.DebugSessionFailed), formatOptionalTime(&completedAt),
		string(task.DebugSessionPending), string(task.DebugSessionRunning))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *DebugRepository) AppendEvent(ctx context.Context, event *task.DebugEvent) error {
	dataJSON := ""
	if len(event.Data) > 0 {
		encoded, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		dataJSON = string(encoded)
	}
	result, err := r.db.ExecContext(ctx, `
INSERT INTO debug_events(session_id, event_type, stage, level, message, elapsed_ms, trace_id, data_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.SessionID, string(event.Type), nullable(event.Stage), string(event.Level), event.Message, event.ElapsedMS, nullable(event.TraceID), nullable(dataJSON), formatTime(event.CreatedAt))
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err == nil {
		event.EventID = id
	}
	return nil
}

// listEventsSQL 按 session_id + event_id 增量拉取，命中 idx_debug_events_session。
const listEventsSQL = `SELECT e.event_id, e.session_id, s.source_id, e.event_type, coalesce(e.stage, ''), e.level, e.message, e.elapsed_ms, coalesce(e.trace_id, ''), coalesce(e.data_json, ''), e.created_at
FROM debug_events e
JOIN debug_sessions s ON s.session_id = e.session_id
WHERE e.session_id = ? AND e.event_id > ?
ORDER BY e.event_id ASC`

func (r *DebugRepository) ListEvents(ctx context.Context, sessionID string, afterEventID int64) ([]*task.DebugEvent, error) {
	rows, err := r.db.QueryContext(ctx, listEventsSQL, sessionID, afterEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []*task.DebugEvent{}
	for rows.Next() {
		event, err := scanDebugEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanDebugSession(s scanner) (*task.DebugSession, error) {
	var session task.DebugSession
	var mode, status string
	var createdAt string
	var completedAt sql.NullString
	err := s.Scan(&session.SessionID, &session.SourceID, &mode, &status, &session.Keyword, &session.URL, &session.TraceID, &createdAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	session.Mode = task.DebugMode(mode)
	session.Status = task.DebugSessionStatus(status)
	session.CreatedAt, err = parseDBTime(createdAt)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid && completedAt.String != "" {
		t, err := parseDBTime(completedAt.String)
		if err != nil {
			return nil, err
		}
		session.CompletedAt = &t
	}
	return &session, nil
}

func scanDebugEvent(s scanner) (*task.DebugEvent, error) {
	var event task.DebugEvent
	var eventType, level string
	var dataJSON string
	var createdAt string
	if err := s.Scan(&event.EventID, &event.SessionID, &event.SourceID, &eventType, &event.Stage, &level, &event.Message, &event.ElapsedMS, &event.TraceID, &dataJSON, &createdAt); err != nil {
		return nil, err
	}
	event.Type = task.DebugEventType(eventType)
	event.Level = task.DebugEventLevel(level)
	if dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &event.Data); err != nil {
			return nil, err
		}
	}
	var err error
	event.CreatedAt, err = parseDBTime(createdAt)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

func parseDBTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}
