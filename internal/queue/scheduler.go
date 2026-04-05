package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/17twenty/rally/internal/domain"
)

// ScheduleHeartbeats enqueues a HeartbeatJobArgs for each AE employee,
// using UniqueOpts to prevent duplicate jobs within the heartbeat period.
func ScheduleHeartbeats(ctx context.Context, client *river.Client[pgx.Tx], employees []domain.Employee) error {
	for _, emp := range employees {
		if emp.Type != "ae" {
			continue
		}

		periodSeconds := 60
		if emp.Config != nil && emp.Config.Runtime.HeartbeatSeconds > 0 {
			periodSeconds = emp.Config.Runtime.HeartbeatSeconds
		}

		_, err := client.Insert(ctx, HeartbeatJobArgs{
			EmployeeID: emp.ID,
			CompanyID:  emp.CompanyID,
		}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{
				ByArgs:   true,
				ByPeriod: time.Duration(periodSeconds) * time.Second,
			},
		})
		if err != nil {
			return fmt.Errorf("schedule heartbeat for employee %s: %w", emp.ID, err)
		}
	}
	return nil
}
