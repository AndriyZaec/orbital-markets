package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenAllowsConcurrentReadsWithForeignKeys(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	reserved, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var foreignKeys int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("concurrent read: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}
