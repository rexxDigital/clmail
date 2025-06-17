package db

import (
	"context"
	"database/sql"
	_ "embed"
	"github.com/rexxDigital/clmail/internal/config"
	"log"
	_ "modernc.org/sqlite"
	"path/filepath"
	"time"
)

//go:embed schema.sql
var ddl string

type Client struct {
	DB *sql.DB
	*Queries
}

func NewClient() (*Client, error) {
	ctx := context.Background()

	configDir, err := config.GetConfigDir()
	if err != nil {
		log.Fatalf("failed to get config dir: %v", err)
	}

	dbPath := filepath.Join(configDir, "db.sqlite")

	dsn := dbPath + "?_journal_mode=WAL&_synchronous=NORMAL&_timeout=10000&_busy_timeout=10000"

	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}

	dbConn.SetMaxOpenConns(1)
	dbConn.SetMaxIdleConns(1)
	dbConn.SetConnMaxLifetime(time.Hour)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=1000;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=10000;",
	}

	for _, pragma := range pragmas {
		if _, err := dbConn.ExecContext(ctx, pragma); err != nil {
			_ = dbConn.Close()
			log.Fatalf("failed to set pragma %s: %v", pragma, err)
		}
	}

	// create tables
	if _, err := dbConn.ExecContext(ctx, ddl); err != nil {
		_ = dbConn.Close()
		log.Fatalf("failed to create tables: %v", err)
	}

	return &Client{
		DB:      dbConn,
		Queries: New(dbConn),
	}, nil
}

func (c *Client) Close() error {
	return c.DB.Close()
}
