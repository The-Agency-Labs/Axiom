package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

func TestCreateUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	t.Setenv("DATABASE_PATH", path)
	t.Setenv("SECRET", "test-secret")

	if err := createUser([]string{"alice", "--admin"}, strings.NewReader("password-1\n")); err != nil {
		t.Fatal(err)
	}
	if err := createUser([]string{"bob"}, strings.NewReader("short\n")); err == nil {
		t.Fatal("short password accepted")
	}
	if err := createUser([]string{"alice"}, strings.NewReader("password-2\n")); err == nil {
		t.Fatal("duplicate username accepted")
	}

	conn, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	u, err := db.New(conn).GetUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin != 1 || u.IsActive != 1 || u.WebhookToken == "" {
		t.Fatalf("user: %+v", u)
	}
	if ok, _ := auth.VerifyPassword("password-1", u.PasswordHash); !ok {
		t.Fatal("stored hash does not verify")
	}
}
