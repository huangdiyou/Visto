package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Config struct {
	Path string
}

func Open(ctx context.Context, config Config) (*sql.DB, error) {
	if strings.TrimSpace(config.Path) == "" {
		return nil, errors.New("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(config.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", config.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// A single writer keeps desktop SQLite behavior predictable. This can be
	// relaxed later when PostgreSQL and independent workers are introduced.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := configure(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func configure(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure sqlite with %q: %w", pragma, err)
		}
	}

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}

	return nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}

		applied, err := migrationApplied(ctx, db, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		if err := applyMigration(ctx, db, version, entry.Name(), string(body)); err != nil {
			return err
		}
	}

	return nil
}

func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration name %q has no version prefix", name)
	}

	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("parse migration version from %q: %w", name, err)
	}

	return version, nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var count int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}

	return count > 0, nil
}

func applyMigration(
	ctx context.Context,
	db *sql.DB,
	version int,
	name string,
	body string,
) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("execute migration %s: %w", name, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
		version,
		name,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}

	return nil
}
