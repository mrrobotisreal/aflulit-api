// Package retention ports the Sumrak T20 ring-buffer retention rule (app side:
// features/backup/retention.ts) so both backup targets stay behaviorally
// identical: keep every backup from the 30 most recent distinct UTC dates
// present in the store, and beyond that window one per calendar month (the
// month's latest). Everything else is prunable.
//
// Deliberately pure over names only: age comes from the UTC timestamp
// embedded in each file name, never from the server clock — a wrong clock
// can never delete something still needed. Names that do not parse are
// never touched.
package retention

import (
	"sort"

	"github.com/mrrobotisreal/aflulit-api/internal/naming"
)

// DefaultDailyDays is the Sumrak T20 window: backups from this many distinct
// recent UTC dates are always kept.
const DefaultDailyDays = 30

// Plan is the outcome of planning retention over a set of file names.
// Keep and Prune preserve the input order and partition the input.
type Plan struct {
	Keep  []string
	Prune []string
}

// PlanRetention partitions names into keep/prune per the ring-buffer rule.
// dailyDays <= 0 falls back to DefaultDailyDays.
func PlanRetention(names []string, dailyDays int) Plan {
	if dailyDays <= 0 {
		dailyDays = DefaultDailyDays
	}

	parsed := make([]*naming.Parsed, 0, len(names))
	for _, n := range names {
		if p := naming.Parse(n); p != nil {
			parsed = append(parsed, p)
		}
	}

	// The newest `dailyDays` distinct date keys present.
	dateSet := map[string]struct{}{}
	for _, p := range parsed {
		dateSet[p.DateKey] = struct{}{}
	}
	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	if len(dates) > dailyDays {
		dates = dates[:dailyDays]
	}
	recent := map[string]struct{}{}
	for _, d := range dates {
		recent[d] = struct{}{}
	}

	keep := map[string]struct{}{}
	// Unparseable names are always kept (never touched).
	for _, n := range names {
		if naming.Parse(n) == nil {
			keep[n] = struct{}{}
		}
	}
	// Latest backup per month among the older-than-window files.
	type keeper struct {
		name string
		ts   int64
	}
	monthly := map[string]keeper{}
	for _, p := range parsed {
		if _, ok := recent[p.DateKey]; ok {
			keep[p.Name] = struct{}{}
			continue
		}
		best, ok := monthly[p.MonthKey]
		if !ok || p.Time.UnixMilli() > best.ts {
			monthly[p.MonthKey] = keeper{name: p.Name, ts: p.Time.UnixMilli()}
		}
	}
	for _, k := range monthly {
		keep[k.name] = struct{}{}
	}

	plan := Plan{Keep: []string{}, Prune: []string{}}
	for _, n := range names {
		if _, ok := keep[n]; ok {
			plan.Keep = append(plan.Keep, n)
		} else {
			plan.Prune = append(plan.Prune, n)
		}
	}
	return plan
}
