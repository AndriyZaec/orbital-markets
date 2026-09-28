package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens a SQLite database and runs migrations.
func Open(path string) (*sql.DB, error) {
	// Apply connection-scoped pragmas through the DSN so every pooled
	// connection gets the same safety and lock-wait settings.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// SQLite permits only one writer. Serialize this single-process deployment
	// in database/sql so concurrent background jobs wait instead of losing writes
	// to SQLITE_BUSY. WAL still keeps transactions efficient on the connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragmas: %w", err)
	}

	// Run migrations
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		db.Close()
		return nil, fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return db, nil
}
