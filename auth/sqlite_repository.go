package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

func NewSqliteRepository(db *sql.DB) Repository {
	return &sqliteRepository{db: sqlx.NewDb(db, "sqlite3")}
}

type sqliteRepository struct {
	db *sqlx.DB
}

// AddUser adds the user. It does nothing if the user exists.
func (s *sqliteRepository) AddUser(ctx context.Context, user *User) error {
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO users (id) VALUES (?)", user.ID); err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}
	return nil
}

func (s *sqliteRepository) GetUser(ctx context.Context, userID string) (*User, error) {
	user := &User{}
	if err := s.db.GetContext(ctx, user, "SELECT * FROM users WHERE id = ?", userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to select user: %w", err)
	}
	return user, nil
}

func (s *sqliteRepository) InsertInvite(ctx context.Context, invite *Invite) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO invites (token, created_by, created_at) VALUES (?, ?, ?)",
		invite.Token, invite.CreatedBy, invite.CreatedAt.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("failed to insert invite: %w", err)
	}
	return nil
}

func (s *sqliteRepository) UseInvite(ctx context.Context, token, userID string, usedAt, notBefore time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invites SET used_by = ?, used_at = ?
		WHERE token = ? AND used_by IS NULL AND created_at >= ?`,
		userID, usedAt.UTC().Format(time.RFC3339), token, notBefore.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("failed to use invite: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to use invite: %w", err)
	}
	return n == 1, nil
}
