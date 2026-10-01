package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenHandlesIsolatesOperationalReadsFromWriterAndAnalytics(t *testing.T) {
	handles, err := OpenHandles(filepath.Join(t.TempDir(), "lanes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer handles.Close()

	if got := handles.Writer.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer MaxOpenConnections = %d, want 1", got)
	}
	if got := handles.Analytics.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("analytics MaxOpenConnections = %d, want 4", got)
	}
	if _, err := handles.Writer.Exec("CREATE TABLE lane_test (value INTEGER); INSERT INTO lane_test VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	tx, err := handles.Writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO lane_test VALUES (2)"); err != nil {
		t.Fatal(err)
	}
	var count int
	analyticsConn, err := handles.Analytics.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	analyticsCtx, analyticsCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer analyticsCancel()
	if err := handles.Analytics.QueryRowContext(analyticsCtx, "SELECT COUNT(*) FROM lane_test").Scan(&count); err != nil {
		t.Fatalf("analytics read blocked by another analytics reader: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handles.Operational.QueryRowContext(ctx, "SELECT COUNT(*) FROM lane_test").Scan(&count); err != nil {
		t.Fatalf("operational read blocked by writer: %v", err)
	}
	if count != 1 {
		t.Fatalf("operational count = %d, want committed count 1", count)
	}
	if err := analyticsConn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := handles.Operational.QueryRowContext(ctx, "SELECT COUNT(*) FROM lane_test").Scan(&count); err != nil {
		t.Fatalf("operational read after commit failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("operational count after commit = %d, want 2", count)
	}

	if _, err := handles.Operational.Exec("INSERT INTO lane_test VALUES (3)"); err == nil {
		t.Fatal("operational reader accepted a write")
	}
	if _, err := handles.Analytics.Exec("INSERT INTO lane_test VALUES (3)"); err == nil {
		t.Fatal("analytics reader accepted a write")
	}
}

func TestOpenSerializesSQLiteAccessWithConnectionPragmas(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if got := database.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}

	var foreignKeys, busyTimeout int
	var journalMode string
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}

func TestOpenReadOnlyIsolatesAnalyticsFromOperationalConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analytics.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE analytics_test (value INTEGER); INSERT INTO analytics_test VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got := reader.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
	var count int
	if err := reader.QueryRow("SELECT COUNT(*) FROM analytics_test").Scan(&count); err != nil || count != 1 {
		t.Fatalf("read count = %d, error = %v", count, err)
	}
	if _, err := reader.Exec("INSERT INTO analytics_test VALUES (2)"); err == nil {
		t.Fatal("read-only analytics connection accepted a write")
	}
	rows, err := reader.Query("SELECT value FROM analytics_test UNION ALL SELECT value FROM analytics_test")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("analytics reader returned no rows")
	}
	if _, err := database.Exec("INSERT INTO analytics_test VALUES (2)"); err != nil {
		t.Fatalf("operational write failed while analytics reader was active: %v", err)
	}
}
