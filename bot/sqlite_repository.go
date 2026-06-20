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

func (s *sqliteRepository) GetEpisodeMessage(ctx context.Context, userID, episodeID string) (*EpisodeMessage, error) {
	var row struct {
		ChatID    int64  `db:"chat_id"`
		MessageID int    `db:"message_id"`
		Log       string `db:"log"`
	}
	if err := s.db.GetContext(ctx, &row,
		"SELECT chat_id, message_id, log FROM episode_messages WHERE user_id = ? AND episode_id = ?",
		userID, episodeID,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to select episode message: %w", err)
	}
	return &EpisodeMessage{ChatID: row.ChatID, MessageID: row.MessageID, Log: row.Log}, nil
}

func (s *sqliteRepository) SaveEpisodeMessage(ctx context.Context, userID, episodeID string, msg *EpisodeMessage) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO episode_messages (user_id, episode_id, chat_id, message_id, log) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET chat_id = excluded.chat_id, message_id = excluded.message_id, log = excluded.log
		`, userID, episodeID, msg.ChatID, msg.MessageID, msg.Log,
	); err != nil {
		return fmt.Errorf("failed to upsert episode message: %w", err)
	}
	return nil
}
