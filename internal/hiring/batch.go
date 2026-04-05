package hiring

import (
	"context"
	"fmt"
	"time"

	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/org"
)

// HireAll iterates PlannedRoles and calls HireAE for each.
// Roles are processed in order (CEO first, then reports).
// Sleeps 500ms between hires to avoid Slack rate limits.
func (h *Hirer) HireAll(ctx context.Context, companyID string, plan *org.OrgPlan, company domain.Company) ([]domain.Employee, error) {
	employees := make([]domain.Employee, 0, len(plan.Roles))
	for i, role := range plan.Roles {
		emp, err := h.HireAE(ctx, companyID, role, company)
		if err != nil {
			return employees, fmt.Errorf("hire %s: %w", role.Role, err)
		}
		employees = append(employees, *emp)
		if i < len(plan.Roles)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return employees, nil
}
