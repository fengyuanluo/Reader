package repositories

import (
	"context"
	"database/sql"
	"errors"

	"reader/internal/domain/source"
)

// SourceVariableRepository 基于 source_variables 表实现书源级脚本状态持久化。
type SourceVariableRepository struct {
	db *sql.DB
}

var _ source.VariableRepository = (*SourceVariableRepository)(nil)

func NewSourceVariableRepository(db *sql.DB) *SourceVariableRepository {
	return &SourceVariableRepository{db: db}
}

func (r *SourceVariableRepository) GetVariable(ctx context.Context, sourceURL string, name string) (string, bool, error) {
	var value sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT value FROM source_variables WHERE book_source_url = ? AND name = ?`, sourceURL, name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value.String, true, nil
}

func (r *SourceVariableRepository) PutVariable(ctx context.Context, sourceURL string, name string, value string) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO source_variables(book_source_url, name, value, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(book_source_url, name) DO UPDATE SET
  value = excluded.value,
  updated_at = CURRENT_TIMESTAMP`, sourceURL, name, value)
	return err
}

func (r *SourceVariableRepository) DeleteVariable(ctx context.Context, sourceURL string, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM source_variables WHERE book_source_url = ? AND name = ?`, sourceURL, name)
	return err
}
