package queue

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Client is the global River job queue client.
var Client *river.Client[pgx.Tx]

// InitQueue sets up the River client backed by pgxpool, runs migrations, and
// registers all workers. It stores the client in the package-level Client var.
func InitQueue(ctx context.Context, pool *pgxpool.Pool, deps WorkerDeps) (*river.Client[pgx.Tx], error) {
	driver := riverpgxv5.New(pool)

	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("rivermigrate.New: %w", err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("rivermigrate.Migrate: %w", err)
	}

	deps.DB = pool
	client, err := river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: NewWorkers(deps),
	})
	if err != nil {
		return nil, fmt.Errorf("river.NewClient: %w", err)
	}

	// Start the client so workers begin processing jobs.
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("river client start: %w", err)
	}

	Client = client
	return client, nil
}
