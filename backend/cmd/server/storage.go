package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/studio"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openStorage never falls back after a database configuration or connection
// error. The DSN remains private, including on startup failure.
func openStorage(caps studio.Capabilities) (*studio.FileStore, *sql.DB, error) {
	switch env("STORAGE_BACKEND", "file") {
	case "file":
		store, err := studio.OpenStore(env("DATA_DIR", "./data"), caps)
		return store, nil, err
	case "postgres":
		if os.Getenv("DATABASE_URL") == "" {
			return nil, nil, errors.New("DATABASE_URL required for PostgreSQL")
		}
		db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
		if err != nil {
			return nil, nil, errors.New("database configuration invalid")
		}
		db.SetMaxOpenConns(16)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err = db.PingContext(ctx); err != nil {
			db.Close()
			return nil, nil, errors.New("database connection unavailable")
		}
		if os.Getenv("STORAGE_AUTO_MIGRATE") == "true" {
			if err = studio.InitPostgres(ctx, db); err != nil {
				db.Close()
				return nil, nil, errors.New("database schema initialization failed")
			}
		}
		if err = studio.CheckPostgres(ctx, db); err != nil {
			db.Close()
			return nil, nil, errors.New("database schema unavailable; run explicit offline migration")
		}
		store, err := studio.OpenPostgresStore(env("DATA_DIR", "./data"), db, caps)
		if err != nil {
			db.Close()
			return nil, nil, err
		}
		return store, db, nil
	default:
		return nil, nil, errors.New("STORAGE_BACKEND must be file or postgres")
	}
}
