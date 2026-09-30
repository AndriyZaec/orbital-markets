package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Handles isolates SQLite workloads by role. Writer is the only write-capable
// pool; operational and analytics reads use independent WAL connections so
// background work cannot starve latency-sensitive live state.
type Handles struct {
	Writer      *sql.DB
	Operational *sql.DB
	Analytics   *sql.DB
}

func OpenHandles(path string) (*Handles, error) {
	writer, err := Open(path)
	if err != nil {
		return nil, err
	}
	operational, err := openReadOnly(path, 4, 1000)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open operational reader: %w", err)
	}
	analytics, err := openReadOnly(path, 1, 250)
	if err != nil {
		operational.Close()
		writer.Close()
		return nil, fmt.Errorf("open analytics reader: %w", err)
	}
	return &Handles{Writer: writer, Operational: operational, Analytics: analytics}, nil
}

func (h *Handles) Close() error {
	if h == nil {
		return nil
	}
	return errors.Join(h.Analytics.Close(), h.Operational.Close(), h.Writer.Close())
}

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

// OpenReadOnly opens an isolated query-only connection. In WAL mode this keeps
// analytical reads from occupying the serialized operational connection.
func OpenReadOnly(path string) (*sql.DB, error) {
	return openReadOnly(path, 1, 250)
}

func openReadOnly(path string, maxConnections, busyTimeoutMS int) (*sql.DB, error) {
	database, err := sql.Open("sqlite", fmt.Sprintf(
		"%s?mode=ro&_pragma=busy_timeout(%d)&_pragma=query_only(ON)", path, busyTimeoutMS,
	))
	if err != nil {
		return nil, fmt.Errorf("open read-only db: %w", err)
	}
	database.SetMaxOpenConns(maxConnections)
	database.SetMaxIdleConns(maxConnections)
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping read-only db: %w", err)
	}
	return database, nil
}
