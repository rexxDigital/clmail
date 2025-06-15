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

	dbConn, err := sql.Open("sqlite", filepath.Join(configDir, "db.sqlite"))
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}
	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)
	dbConn.SetConnMaxLifetime(time.Hour)

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
