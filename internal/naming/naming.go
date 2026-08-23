// Package naming parses Sumrak backup file names. The name format is the
// T20 contract (Sumrak app, features/backup/naming.ts): the snapshot moment
// is embedded in UTC so retention can reason about age from the name alone —
// server-clock independent, and identical across every backup target.
package naming

import (
	"regexp"
	"time"
)

// BackupFileRe matches "sumrak-backup-YYYYMMDD-HHMMSSZ.json".
var BackupFileRe = regexp.MustCompile(`^sumrak-backup-(\d{8})-(\d{6})Z\.json$`)

// Parsed describes one well-formed backup name.
type Parsed struct {
	Name string
	// DateKey is "YYYY-MM-DD" (UTC).
	DateKey string
	// MonthKey is "YYYY-MM" (UTC).
	MonthKey string
	// Time is the stamped moment (UTC).
	Time time.Time
}

// Parse returns nil for anything that is not a well-formed backup name,
// including digit patterns with impossible calendar values (month 13,
// Feb 30). Files that do not parse are never pruned and never served.
func Parse(name string) *Parsed {
	m := BackupFileRe.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	// time.Parse rejects out-of-range calendar values outright.
	t, err := time.Parse("20060102-150405", m[1]+"-"+m[2])
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &Parsed{
		Name:     name,
		DateKey:  t.Format("2006-01-02"),
		MonthKey: t.Format("2006-01"),
		Time:     t,
	}
}

// Valid reports whether name is a well-formed backup file name.
func Valid(name string) bool {
	return Parse(name) != nil
}
