package retention

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/mrrobotisreal/sumrak-api/internal/naming"
)

// The cases below are ports of the T20 app-side suite
// (Sumrak features/backup/__tests__/naming-retention.test.ts) — the two
// implementations must stay behaviorally identical.

func nameFor(dateKey, timePart string) string {
	if timePart == "" {
		timePart = "030000"
	}
	d := ""
	for _, r := range dateKey {
		if r != '-' {
			d += string(r)
		}
	}
	return fmt.Sprintf("sumrak-backup-%s-%sZ.json", d, timePart)
}

func fileNameAt(t time.Time) string {
	return fmt.Sprintf("sumrak-backup-%sZ.json", t.UTC().Format("20060102-150405"))
}

func TestNamingRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 23, 3, 7, 9, 0, time.UTC)
	name := fileNameAt(at)
	if name != "sumrak-backup-20260823-030709Z.json" {
		t.Fatalf("unexpected name %q", name)
	}
	p := naming.Parse(name)
	if p == nil {
		t.Fatal("parse returned nil")
	}
	if p.DateKey != "2026-08-23" || p.MonthKey != "2026-08" || !p.Time.Equal(at) {
		t.Fatalf("bad parse: %+v", p)
	}
}

func TestNamingRejects(t *testing.T) {
	for _, bad := range []string{
		"manifest.json",
		"sumrak-backup-20260823-030709.json", // no Z
		nameFor("2026-13-01", ""),            // month 13
		nameFor("2026-02-30", ""),            // Feb 30
		"../etc/passwd",
		"sumrak-backup-20260823-030709Z.json.bak",
	} {
		if naming.Parse(bad) != nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestKeepsEverythingUnder30Days(t *testing.T) {
	names := []string{
		nameFor("2026-08-01", ""), nameFor("2026-08-02", ""), nameFor("2026-08-03", ""),
	}
	plan := PlanRetention(names, DefaultDailyDays)
	if len(plan.Prune) != 0 {
		t.Fatalf("expected no prunes, got %v", plan.Prune)
	}
	if !reflect.DeepEqual(plan.Keep, names) {
		t.Fatalf("keep mismatch: %v", plan.Keep)
	}
}

func TestNewest30DatesPlusMonthlyKeeper(t *testing.T) {
	// 40 consecutive days: 2026-07-20 .. 2026-08-28.
	names := []string{}
	for i := 0; i < 40; i++ {
		names = append(names, fileNameAt(time.Date(2026, 7, 20+i, 0, 0, 0, 0, time.UTC)))
	}
	plan := PlanRetention(names, DefaultDailyDays)

	keptDates := []string{}
	for _, n := range plan.Keep {
		keptDates = append(keptDates, naming.Parse(n).DateKey)
	}
	sort.Strings(keptDates)

	recent := 0
	older := []string{}
	for _, d := range keptDates {
		if d >= "2026-07-30" {
			recent++
		} else {
			older = append(older, d)
		}
	}
	if recent != DefaultDailyDays {
		t.Fatalf("expected %d recent dates kept, got %d", DefaultDailyDays, recent)
	}
	if !reflect.DeepEqual(older, []string{"2026-07-29"}) {
		t.Fatalf("expected only 2026-07-29 kept from the older window, got %v", older)
	}
	if len(plan.Prune) != 9 {
		t.Fatalf("expected 9 prunes, got %d", len(plan.Prune))
	}
	if len(plan.Keep)+len(plan.Prune) != len(names) {
		t.Fatal("keep+prune must partition the input")
	}
}

func TestOneMonthlyPerDistinctOlderMonth(t *testing.T) {
	old := []string{
		nameFor("2026-01-05", "020000"),
		nameFor("2026-01-20", "020000"),
		nameFor("2026-02-03", "020000"),
		nameFor("2026-02-27", "020000"),
	}
	recent := []string{nameFor("2026-08-20", ""), nameFor("2026-08-21", ""), nameFor("2026-08-22", "")}

	// 7 distinct dates < 30 → the window covers all of them, nothing pruned.
	if plan := PlanRetention(append(append([]string{}, old...), recent...), DefaultDailyDays); len(plan.Prune) != 0 {
		t.Fatalf("expected no prunes, got %v", plan.Prune)
	}

	// 30 fresh dates push the old months outside the window.
	fresh := []string{}
	for i := 0; i < 30; i++ {
		fresh = append(fresh, fileNameAt(time.Date(2026, 8, 1+i, 3, 0, 0, 0, time.UTC)))
	}
	plan := PlanRetention(append(append([]string{}, old...), fresh...), DefaultDailyDays)

	olderKept := []string{}
	for _, n := range plan.Keep {
		if n < "sumrak-backup-202608" {
			olderKept = append(olderKept, n)
		}
	}
	sort.Strings(olderKept)
	wantKept := []string{nameFor("2026-01-20", "020000"), nameFor("2026-02-27", "020000")}
	if !reflect.DeepEqual(olderKept, wantKept) {
		t.Fatalf("older keepers mismatch: got %v want %v", olderKept, wantKept)
	}
	prune := append([]string{}, plan.Prune...)
	sort.Strings(prune)
	wantPrune := []string{nameFor("2026-01-05", "020000"), nameFor("2026-02-03", "020000")}
	if !reflect.DeepEqual(prune, wantPrune) {
		t.Fatalf("prune mismatch: got %v want %v", prune, wantPrune)
	}
}

func TestMultipleBackupsOneRecentDayAllKept(t *testing.T) {
	names := []string{nameFor("2026-08-23", "010000"), nameFor("2026-08-23", "230000")}
	if plan := PlanRetention(names, DefaultDailyDays); len(plan.Prune) != 0 {
		t.Fatalf("expected no prunes, got %v", plan.Prune)
	}
}

func TestNeverTouchesUnparseableAndNewest(t *testing.T) {
	names := []string{"README.md"}
	for i := 0; i < 45; i++ {
		names = append(names, fileNameAt(time.Date(2026, 4, 1+i, 0, 0, 0, 0, time.UTC)))
	}
	plan := PlanRetention(names, DefaultDailyDays)
	keep := map[string]bool{}
	for _, n := range plan.Keep {
		keep[n] = true
	}
	if !keep["README.md"] {
		t.Fatal("unparseable name must always be kept")
	}
	if !keep[names[len(names)-1]] {
		t.Fatal("newest backup must always be kept")
	}
}

func TestClockIndependent(t *testing.T) {
	names := []string{
		nameFor("2030-01-01", ""), // wrong-clock artifact
		nameFor("2026-08-22", ""),
		nameFor("2026-08-23", ""),
	}
	if plan := PlanRetention(names, DefaultDailyDays); len(plan.Prune) != 0 {
		t.Fatalf("expected no prunes, got %v", plan.Prune)
	}
}
