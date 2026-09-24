package service

import (
	"context"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

// DashboardService is the data boundary for all authenticated screens.
type DashboardService interface {
	GetOverview(context.Context, domain.Filter) (domain.Dashboard, error)
	GetSavings(context.Context, domain.Filter, string) (domain.Dashboard, error)
	GetDeposits(context.Context, domain.Filter, string) (domain.Dashboard, error)
	GetDepositMaturities(context.Context, domain.Filter) (domain.Dashboard, error)
	GetLoans(context.Context, domain.Filter) (domain.Dashboard, error)
	GetFinancialPerformance(context.Context, domain.Filter) (domain.Dashboard, error)
	GetNominative(context.Context, domain.NominativeFilter) (domain.NominativeResult, error)
}
