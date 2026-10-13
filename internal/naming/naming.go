// Package naming parses Aflulit backup file names. The name format is the
// contract shared with the app (Aflulit features/backup/naming.ts, inherited
// from Sumrak T20): the snapshot moment is embedded in UTC so retention can
// reason about age from the name alone — server-clock independent, and
// identical across every backup target.
//
// Since Sumrak T63 a second family shares the store: scenario media bundles
// "aflulit-media-<runId>.json" — immutable, one per finished run, never
// pruned (design SPEAKING_SCENARIOS §10.3). They are valid names for Put /
// Open / List but are invisible to retention, which only ever plans over
// snapshot names.
package naming

import (
	"regexp"
	"time"
)

// BackupFileRe matches "aflulit-backup-YYYYMMDD-HHMMSSZ.json" (ADR-0002).
var BackupFileRe = regexp.MustCompile(`^aflulit-backup-(\d{8})-(\d{6})Z\.json$`)

// MediaFileRe matches "aflulit-media-<runId>.json" (Sumrak T63): the run id is the
// app's row id (base36 time + random, [a-z0-9-]) — no separators, so the
// name doubles as the path-traversal guard like the snapshot names do.
var MediaFileRe = regexp.MustCompile(`^aflulit-media-([a-z0-9][a-z0-9-]{3,79})\.json$`)

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

// Valid reports whether name is a well-formed backup file name — a snapshot
// or (Sumrak T63) a media bundle.
func Valid(name string) bool {
	return Parse(name) != nil || IsMedia(name)
}

// IsMedia reports whether name is a well-formed media bundle name (Sumrak T63).
func IsMedia(name string) bool {
	return MediaFileRe.MatchString(name)
}
