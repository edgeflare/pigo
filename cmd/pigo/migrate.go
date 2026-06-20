package pigo

import (
	"bytes"
	"cmp"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	"github.com/jackc/pgx/v5"
	tern "github.com/jackc/tern/v2/migrate"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

//go:embed migrations/*
var migrations embed.FS

type migrationConfig struct {
	Host         string         `json:"host"`
	Port         int            `json:"port"`
	Database     string         `json:"database"`
	User         string         `json:"user"`
	Password     string         `json:"password"`
	SSLMode      string         `json:"sslmode"`
	ConnString   string         `json:"conn_string"`
	VersionTable string         `json:"version_table"`
	Data         map[string]any `json:"data"`
}

var migrateCmd = &cobra.Command{
	Use:          "migrate",
	Short:        "Run database migrations",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		for i, a := range os.Args {
			if a == "migrate" {
				return migrate(cmd.Context(), os.Args[i+1:])
			}
		}
		return migrate(cmd.Context(), nil)
	},
}

func init() {
	f := migrateCmd.Flags()
	f.String("tern-config", "", "migration config file (env: TERN_CONFIG)")
	f.String("migrations", ".", "migrations directory (env: TERN_MIGRATIONS)")
	f.String("version-table", "", "version table (overrides config)")
	f.String("conn-string", "", "connection string (overrides config)")
	f.String("host", "", "database host")
	f.Int("port", 0, "database port")
	f.String("database", "", "database name")
	f.String("user", "", "database user")
	f.String("password", "", "database password")
	f.String("sslmode", "", "SSL mode")
	f.String("destination", "", "target version (42, +3, -3, -+3; empty = latest)")
	rootCmd.AddCommand(migrateCmd)
}

// TODO: drop cobra dependency
func migrate(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("migrate", flag.ContinueOnError)
	configPath := fset.String("tern-config", cmp.Or(os.Getenv("TERN_CONFIG"), "tern.yaml"), "")
	migrationsDir := fset.String("migrations", cmp.Or(os.Getenv("TERN_MIGRATIONS"), "."), "")
	versionTable := fset.String("version-table", "", "")
	connString := fset.String("conn-string", "", "")
	host := fset.String("host", "", "")
	port := fset.Int("port", 0, "")
	database := fset.String("database", "", "")
	user := fset.String("user", "", "")
	password := fset.String("password", "", "")
	sslmode := fset.String("sslmode", "", "")
	destination := fset.String("destination", "", "")
	if err := fset.Parse(args); err != nil {
		return err
	}

	cfg, err := loadMigrationConfig(*configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.ConnString = cmp.Or(*connString, cfg.ConnString, os.Getenv("PIGO_CONN_STRING"))
	cfg.Host = cmp.Or(*host, cfg.Host, os.Getenv("PGHOST"), "localhost")
	cfg.Port = cmp.Or(*port, cfg.Port, func() int { v, _ := strconv.Atoi(os.Getenv("PGPORT")); return v }(), 5432)
	cfg.Database = cmp.Or(*database, cfg.Database, os.Getenv("PGDATABASE"))
	cfg.User = cmp.Or(*user, cfg.User, os.Getenv("PGUSER"), os.Getenv("USER"))
	cfg.Password = cmp.Or(*password, cfg.Password, os.Getenv("PGPASSWORD"))
	cfg.SSLMode = cmp.Or(*sslmode, cfg.SSLMode, os.Getenv("PGSSLMODE"), "prefer")
	cfg.VersionTable = cmp.Or(*versionTable, cfg.VersionTable, "public.schema_version")

	uri := cmp.Or(cfg.ConnString, fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.Password, cfg.SSLMode))
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var fsys fs.FS
	if *migrationsDir == "." {
		fsys, err = fs.Sub(migrations, "migrations")
	} else {
		fsys = os.DirFS(*migrationsDir)
	}
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}

	m, err := tern.NewMigrator(ctx, conn, cfg.VersionTable)
	if err != nil {
		return fmt.Errorf("new migrator: %w", err)
	}
	m.OnStart = func(seq int32, name, direction, _ string) {
		slog.Info("migration", "version", seq, "name", name, "direction", direction)
	}
	if cfg.Data != nil {
		m.Data = cfg.Data
	}
	if err := m.LoadMigrations(fsys); err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	return migrateTo(ctx, m, *destination)
}

func loadMigrationConfig(path string) (migrationConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return migrationConfig{}, err
	}
	tmpl, err := template.New("").Funcs(sprig.TxtFuncMap()).Funcs(template.FuncMap{"env": os.Getenv}).Parse(string(raw))
	if err != nil {
		return migrationConfig{}, fmt.Errorf("parse config template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return migrationConfig{}, fmt.Errorf("execute config template: %w", err)
	}
	var cfg migrationConfig
	return cfg, yaml.Unmarshal(buf.Bytes(), &cfg)
}

func migrateTo(ctx context.Context, m *tern.Migrator, dest string) error {
	if dest == "" {
		return m.Migrate(ctx)
	}

	n, err := strconv.Atoi(strings.TrimLeft(dest, "-+"))
	if err != nil {
		return fmt.Errorf("invalid destination %q", dest)
	}

	cur := func() (int32, error) { return m.GetCurrentVersion(ctx) }
	to := func(v int32) error { return m.MigrateTo(ctx, v) }

	switch {
	case strings.HasPrefix(dest, "-+"):
		v, err := cur()
		if err != nil {
			return err
		}
		if err := to(v - int32(n)); err != nil {
			return err
		}
		return to(v)
	case strings.HasPrefix(dest, "+"):
		v, err := cur()
		if err != nil {
			return err
		}
		return to(v + int32(n))
	case strings.HasPrefix(dest, "-"):
		v, err := cur()
		if err != nil {
			return err
		}
		return to(v - int32(n))
	default:
		return to(int32(n))
	}
}
