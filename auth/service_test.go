package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	migrate "github.com/rubenv/sql-migrate"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // each :memory: connection is a separate database
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../db/migrations"}, migrate.Up); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}
	return New("admin", NewSqliteRepository(db), slog.New(slog.DiscardHandler))
}

func TestInviteGivesAccessOnce(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	invite, err := svc.CreateInvite(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}

	if ok, _ := svc.IsAuthenticated(ctx, "42", "friend"); ok {
		t.Fatal("user has access before the invite")
	}
	if err := svc.AcceptInvite(ctx, invite.Token, "42"); err != nil {
		t.Fatalf("AcceptInvite() = %v", err)
	}
	if ok, err := svc.IsAuthenticated(ctx, "42", "friend"); err != nil || !ok {
		t.Fatalf("IsAuthenticated() = %v, %v; want true", ok, err)
	}

	if err := svc.AcceptInvite(ctx, invite.Token, "43"); !errors.Is(err, ErrInviteNotValid) {
		t.Fatalf("second AcceptInvite() = %v, want ErrInviteNotValid", err)
	}
	if err := svc.AcceptInvite(ctx, "no-such-token", "44"); !errors.Is(err, ErrInviteNotValid) {
		t.Fatalf("AcceptInvite(unknown) = %v, want ErrInviteNotValid", err)
	}
}

func TestEmptyUsernameIsNotAdmin(t *testing.T) {
	svc := New("", nil, slog.New(slog.DiscardHandler))
	if ok, _ := svc.IsAdmin(context.Background(), ""); ok {
		t.Fatal("empty username is admin when ADMIN_USERNAME is empty")
	}
}
