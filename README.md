# Roro Jongrang Management Dashboard

An internal BPR management dashboard with selectable mock and read-only DWH data sources. HTTP handlers depend on `service.DashboardService`; implementations live in `internal/service/mock` and `internal/service/dwh`.

## Run

Requires Go 1.26. From the repository root:

```sh
cp .env.example .env
go mod download
go run ./cmd/web
```

Open <http://localhost:8080/login>. `ADDR` changes the listening address. `DASHBOARD_DATA_SOURCE=mock` is the default and needs no database. Set `DASHBOARD_DATA_SOURCE=dwh` and provide `DWH_DBSTRING` in the runtime environment to use the external DWH. The DWH connection has its own pool and every application query runs in a read-only transaction. Never point a writable application database or migrations at the DWH. ECharts 5.6.0 and HTMX 2.0.8 browser bundles are served from `web/static/js`; their licenses and ECharts notice are stored beside them.

| Demo user | Password | Access |
| --- | --- | --- |
| `admin` | `admin` | Consolidated view and branches 000–008 |
| `branch001` | `demo` | Branch 001 only |

Authentication uses in-memory opaque sessions and fixed demo credentials. Sessions end when the process restarts. Replace this component before production use, including when viewing real DWH data.

## Routes

| Route | Content |
| --- | --- |
| `/dashboard` | Management overview and funding/loan trends |
| `/tabungan` | DPK and ABP savings tabs |
| `/deposito` | ABP, DPK, and maturity tabs |
| `/kredit` | Channeling and organik comparison |
| `/kinerja` | Ratios, assets, and profit |
| `/nominatif` | Searchable account detail reached from KPI cards or maturity rows |
| `/login`, `/logout` | Demo sign-in and sign-out |

Filters are bookmarkable query parameters: `mode=daily|monthly|yearly`, `period=YYYY-MM-DD|YYYY-MM|YYYY`, and `branch=ALL|000..008`. Nominative links also use `domain`, `category`, `metric`, optional `bucket`, `search`, and `page`. A single-branch user cannot request another branch by editing a URL.

## Architecture and mock rules

`cmd/web` selects a `DashboardService`; handlers parse the shared filter and ask the service for page DTOs; `internal/view` formats and renders those DTOs. Templates contain no banking figures. The mock service starts from seeded account records and deterministic daily balances, booking flows, maturity dates, and illustrative financial facts. KPI values, historical points, branch consolidation, and account tables use those records. Maturity bucket links apply the same bucket to the nominative table, so their NOA and nominal totals reconcile. Search shows a filtered subtotal separately from the parent total.

Demo history runs from 1 September 2024 through 24 September 2026. Daily mode compares with the previous calendar date. Monthly and yearly balance figures are as-of snapshots; booking and profit are period flows. The current open month/year compares flows with the matching cutoff in the previous month/year. Dates outside the demo range show an empty state. Rupiah values use `int64`; ratios use hundredths of a percentage point for display.

DWH table mappings, branch scope, formulas, sampled data shape, and the owner's blocked ABA assumption are recorded in [the DWH audit](docs/DWH_AUDIT.md). The real source uses the latest common DWH snapshot as the default date. Monthly and yearly positions select one reporting-date snapshot; Booking uses the selected period. Cash Ratio currently assumes blocked ABA is zero, as directed by the dashboard owner.

The mock's financial facts remain illustrative; the DWH service uses the audited Fincloud mappings. Direction changes remain neutral.

## Checks

```sh
gofmt -w cmd internal
go vet ./...
go test ./...
```

The tests cover period cutoffs, branch consolidation, KPI comparisons, nominated totals and maturity buckets, search/pagination, formatting, and authenticated route behavior.
