package payment

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
)

type memoryPurchaseGranter interface {
	GrantPurchasePlanMemory(context.Context, string, string, int, string) error
}

func (m *MemoryStore) grantPaid(ctx context.Context, oid string, credits memoryPurchaseGranter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[oid]
	if !ok {
		return ErrNotFound
	}
	if o.Granted {
		return nil
	}
	if o.State != StatePaid {
		return pkgerrors.ErrConflict
	}
	plan := ""
	if o.Kind == "plan" {
		if billing.DefaultPlans()[o.SKUCode] == nil {
			return pkgerrors.ErrConflict
		}
		plan = o.SKUCode
	}
	if err := credits.GrantPurchasePlanMemory(ctx, o.TenantID, o.ID, o.Credits, plan); err != nil {
		return err
	}
	o.Granted = true
	return nil
}
