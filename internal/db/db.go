package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Open creates a new connection pool using the given DATABASE_URL.
func Open(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Ping verifies connectivity to the database.
func (db *DB) Ping(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}

// Close closes the connection pool.
func (db *DB) Close() {
	db.Pool.Close()
}
