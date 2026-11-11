// Package database opens the PostgreSQL connection (GORM on top of pgx), provides
// transaction helpers, and runs schema migrations.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"atlas-backend-core/internal/config"
)

// Open connects to PostgreSQL through the pgx driver, configures the connection pool from
// cfg, verifies connectivity, and returns a GORM handle.
//
// GORM is configured to:
//   - translate driver errors into gorm.ErrDuplicatedKey / gorm.ErrForeignKeyViolated,
//   - skip the implicit transaction around single writes (use Transactor when needed),
//   - log slow queries and errors through slog without bound parameter values.
//
// Release the pool with Close when the application stops.
func Open(ctx context.Context, cfg config.DBConfig, log *slog.Logger) (*gorm.DB, error) {
	connCfg, err := pgx.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	connCfg.ConnectTimeout = cfg.ConnectTimeout

	sqlDB := stdlib.OpenDB(*connCfg)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.MaxConnLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.MaxConnIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	logLevel := gormlogger.Warn
	if cfg.LogQueries {
		logLevel = gormlogger.Info
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger: gormlogger.NewSlogLogger(log, gormlogger.Config{
			SlowThreshold:             cfg.SlowQueryThreshold,
			LogLevel:                  logLevel,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
		TranslateError:         true,
		SkipDefaultTransaction: true,
		NowFunc:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open gorm: %w", err)
	}
	return db, nil
}

// Close releases the connection pool behind db.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	return sqlDB.Close()
}
