package bot

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

func NewSqliteRepository(db *sql.DB) Repository {
	return &sqliteRepository{db: sqlx.NewDb(db, "sqlite3")}
}

type sqliteRepository struct {
	db *sqlx.DB
}

func (s *sqliteRepository) SetChatID(ctx context.Context, userID string, chatID int64) error {
	result := s.db.MustExecContext(ctx, `
		INSERT INTO chats (user_id, chat_id) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET chat_id = ?
		`, userID, chatID, chatID,
	)
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("failed to insert chat: %w", err)
	}
	return nil
}

func (s *sqliteRepository) GetChatID(ctx context.Context, userID string) (int64, error) {
	var chatID int64
	if err := s.db.GetContext(ctx, &chatID, "SELECT chat_id FROM chats WHERE user_id = ?", userID); err != nil {
		if err == sql.ErrNoRows {
			return -1, nil
		}
		return -1, fmt.Errorf("failed to select chat: %w", err)
	}
	return chatID, nil
}
