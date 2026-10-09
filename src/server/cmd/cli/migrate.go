// Provides explicit operator migration bootstrap using the existing PostgreSQL runner.
// Input: trusted migration directory and environment DSN; output: JSON success only
// after checksum-verified transactional replay/apply. No application startup migration,
// down/reset or credential logging. Timeout bounds connection, lock and SQL execution;
// measure migration/lock time separately from required query latency benchmarks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"regulagraph.local/server/internal/adapters/postgres"
)

type migrationApply func(context.Context, string, fs.FS) error

func runMigrate(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runMigrateWith(ctx, args, out, errOut, func(ctx context.Context, dsn string, files fs.FS) error {
		repo, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 1, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
		if err != nil {
			return err
		}
		defer repo.Close()
		return repo.ApplyMigrations(ctx, files)
	})
}

func runMigrateWith(ctx context.Context, args []string, out, errOut io.Writer, apply migrationApply) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(errOut)
	directory := flags.String("dir", "migrations", "Trusted directory of ordered *.up.sql migrations")
	timeout := flags.Duration("timeout", 5*time.Minute, "Total connection/lock/migration deadline, 1s..30m")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *directory == "" || *timeout < time.Second || *timeout > 30*time.Minute {
		fmt.Fprintln(errOut, "Invalid migration directory, timeout or positional arguments")
		return 2
	}
	dsn := os.Getenv("REGULAGRAPH_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		fmt.Fprintln(errOut, "REGULAGRAPH_POSTGRES_DSN is required")
		return 2
	}
	files := os.DirFS(*directory)
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		fmt.Fprintln(errOut, "Cannot read migration directory")
		return 2
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			count++
		}
	}
	if count == 0 {
		fmt.Fprintln(errOut, "Migration directory contains no *.up.sql files")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err = apply(bounded, dsn, files); err != nil {
		// Backend errors may contain connection strings or SQL; preserve only safe categories.
		reason := "database or migration failure; inspect server logs and migration checksums"
		if errors.Is(err, postgres.ErrConflict) {
			reason = "migration checksum conflict; restore original migration bytes"
		}
		if bounded.Err() != nil {
			reason = "migration cancelled or timed out; replay identical files to resume"
		}
		fmt.Fprintln(errOut, reason)
		return 1
	}
	if err = json.NewEncoder(out).Encode(struct {
		Status string `json:"status"`
		Files  int    `json:"migration_files"`
	}{"applied", count}); err != nil {
		fmt.Fprintln(errOut, "Migration completed but output failed; replay identical files to confirm")
		return 1
	}
	return 0
}
