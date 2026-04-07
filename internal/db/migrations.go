package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
)

func RunMigrations(ctx context.Context, repo Repository, root fs.FS) error {
	dir, err := migrationDir(root, repo.Driver())
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return fmt.Errorf("read migration dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	if _, err := repo.DB().ExecContext(ctx, migrationBootstrapSQL(repo.Driver())); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	for _, name := range names {
		applied, err := migrationApplied(ctx, repo, name)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		path := filepath.Join(dir, name)
		body, err := fs.ReadFile(root, path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}

		tx, err := repo.DB().BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("execute migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, migrationInsertSQL(repo.Driver()), name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}

func migrationDir(root fs.FS, driver string) (string, error) {
	candidates := []string{
		filepath.Join("migrations", driver),
		driver,
	}

	for _, dir := range candidates {
		entries, err := fs.ReadDir(root, dir)
		if err == nil {
			if len(entries) == 0 {
				return "", fmt.Errorf("read migration dir %s: empty directory", dir)
			}
			return dir, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("read migration dir %s: %w", dir, err)
		}
	}

	return "", fmt.Errorf("read migration dir %s: %w", filepath.Join("migrations", driver), fs.ErrNotExist)
}

func migrationApplied(ctx context.Context, repo Repository, name string) (bool, error) {
	var exists bool
	err := repo.DB().QueryRowContext(ctx, migrationExistsSQL(repo.Driver()), name).Scan(&exists)
	return exists, err
}

func migrationBootstrapSQL(driver string) string {
	if driver == "sqlite" {
		return `create table if not exists schema_migrations (name text primary key, applied_at text not null default current_timestamp);`
	}
	return `create table if not exists schema_migrations (name text primary key, applied_at timestamptz not null default now());`
}

func migrationExistsSQL(driver string) string {
	if driver == "sqlite" {
		return `select exists(select 1 from schema_migrations where name = ?);`
	}
	return `select exists(select 1 from schema_migrations where name = $1);`
}

func migrationInsertSQL(driver string) string {
	if driver == "sqlite" {
		return `insert into schema_migrations (name) values (?);`
	}
	return `insert into schema_migrations (name) values ($1);`
}
