package auth

import (
	"context"
	"database/sql"
	"github.com/hori-ryota/zaperr"
	"github.com/jmoiron/sqlx"
)

func NewSqliteRepository(db *sql.DB) Repository {
	return &sqliteRepository{db: sqlx.NewDb(db, "sqlite3")}
}

type sqliteRepository struct {
	db *sqlx.DB
}

func (s *sqliteRepository) AddUser(ctx context.Context, user *User) error {
	result := s.db.MustExecContext(ctx, "INSERT INTO users (id) VALUES (?)", user.ID)
	if _, err := result.RowsAffected(); err != nil {
		return zaperr.Wrap(err, "failed to insert user")
	}
	return nil
}

func (s *sqliteRepository) GetUser(ctx context.Context, userID string) (*User, error) {
	user := &User{}
	if err := s.db.GetContext(ctx, user, "SELECT * FROM users WHERE id = ?", userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, zaperr.Wrap(err, "failed to select user")
	}
	return user, nil
}
