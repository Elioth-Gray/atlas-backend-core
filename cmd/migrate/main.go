// Command migrate manages the database schema using the migrations embedded from
// ./migrations.
//
//	go run ./cmd/migrate up            apply all pending migrations
//	go run ./cmd/migrate down [N]      roll back the last N migrations (default 1)
//	go run ./cmd/migrate version       print the current schema version
//	go run ./cmd/migrate force V       mark version V as applied (fixes a dirty state)
//	go run ./cmd/migrate create NAME   create a new up/down pair in ./migrations
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/database"
)

const (
	migrationsDir = "migrations"
	usage         = `usage: migrate <command> [arguments]

commands:
  up            apply all pending migrations
  down [N]      roll back the last N migrations (default 1)
  version       print the current schema version
  force V       mark version V as applied without running it (fixes a dirty state)
  create NAME   create a new up/down migration pair in ./migrations
`
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) (err error) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("missing command")
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "create":
		return createMigration(migrationsDir, rest)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	m, err := database.NewMigrator(cfg.DB.URL)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.CloseMigrator(m)) }()

	switch cmd {
	case "up":
		if err := ignoreNoChange(m.Up()); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case "down":
		steps, err := optionalPositiveInt(rest, 1)
		if err != nil {
			return err
		}
		if err := ignoreNoChange(m.Steps(-steps)); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	case "force":
		if len(rest) != 1 {
			return errors.New("usage: migrate force VERSION")
		}
		version, err := strconv.Atoi(rest[0])
		if err != nil {
			return fmt.Errorf("invalid version %q: %w", rest[0], err)
		}
		if err := m.Force(version); err != nil {
			return fmt.Errorf("force version: %w", err)
		}
	case "version":
		// handled below
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
	return printVersion(m)
}

func printVersion(m *migrate.Migrate) error {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("schema version: none (no migrations applied)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	fmt.Printf("schema version: %d (dirty: %t)\n", version, dirty)
	return nil
}

func ignoreNoChange(err error) error {
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("no change")
		return nil
	}
	return err
}

func optionalPositiveInt(args []string, def int) (int, error) {
	if len(args) == 0 {
		return def, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("expected a positive number, got %q", args[0])
	}
	return n, nil
}

// createMigration writes NNNNNN_name.up.sql and NNNNNN_name.down.sql using the next free
// sequence number in dir.
func createMigration(dir string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: migrate create NAME")
	}
	name := sanitizeName(args[0])
	if name == "" {
		return fmt.Errorf("invalid migration name %q", args[0])
	}

	next, err := nextVersion(dir)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("%06d_%s", next, name)
	for _, direction := range []string{"up", "down"} {
		path := filepath.Join(dir, base+"."+direction+".sql")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // migration files are committed source code
		if err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		_, werr := fmt.Fprintf(f, "-- %s (%s)\n", base, direction)
		if err := errors.Join(werr, f.Close()); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Println("created", path)
	}
	return nil
}

func nextVersion(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	next := 1
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(prefix); err == nil && n >= next {
			next = n + 1
		}
	}
	return next, nil
}

func sanitizeName(s string) string {
	var b strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
