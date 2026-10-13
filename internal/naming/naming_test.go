package naming

import (
	"strings"
	"testing"
)

// THE NAME CONTRACT (T11). This table is mirrored literally in the app's
// features/backup/__tests__/naming-retention.test.ts ("syncd name contract")
// — keep the two in sync: a rename or a regex change on either side must
// break both tests. The name is also the path-traversal guard, so every
// separator case here is a security case.
var nameContract = []struct {
	name   string
	valid  bool
	media  bool
	reason string
}{
	// Snapshots.
	{"aflulit-backup-20260823-030709Z.json", true, false, "canonical snapshot"},
	{"aflulit-backup-20261231-235959Z.json", true, false, "last second of a year"},
	{"aflulit-backup-20260823-030709.json", false, false, "missing Z"},
	{"aflulit-backup-20260823-030709Z.json.bak", false, false, "trailing suffix"},
	{"aflulit-backup-2026082-030709Z.json", false, false, "seven-digit date"},
	{"aflulit-backup-20261301-030709Z.json", false, false, "month 13"},
	{"aflulit-backup-20260230-030709Z.json", false, false, "Feb 30"},
	{"aflulit-backup-20260823-240000Z.json", false, false, "hour 24"},
	{"aflulit-backup-20260823-036000Z.json", false, false, "minute 60"},
	{"sumrak-backup-20260823-030709Z.json", false, false, "old product prefix"},
	{"AFLULIT-BACKUP-20260823-030709Z.json", false, false, "uppercase prefix"},
	// Media bundles.
	{"aflulit-media-mg1x2k3a-abc12def00.json", true, true, "scenario run id"},
	{"aflulit-media-exam-mkxy1abc-q7ztuv00.json", true, true, "exam attempt stem"},
	{"aflulit-media-abcd.json", true, true, "4-char run id (minimum)"},
	{"aflulit-media-" + strings.Repeat("a", 80) + ".json", true, true, "80-char run id (maximum)"},
	{"aflulit-media-abc.json", false, false, "3-char run id (too short)"},
	{"aflulit-media-" + strings.Repeat("a", 81) + ".json", false, false, "81-char run id (too long)"},
	{"aflulit-media--abcd.json", false, false, "run id starting with a dash"},
	{"aflulit-media-ABCD-efgh.json", false, false, "uppercase run id"},
	{"aflulit-media-ab_cd.json", false, false, "underscore in run id"},
	{"aflulit-media-../x.json", false, false, "traversal in run id"},
	{"sumrak-media-mg1x2k3a-abc12def00.json", false, false, "old product prefix"},
	// Strangers and traversal.
	{"", false, false, "empty"},
	{"manifest.json", false, false, "manifest"},
	{"../etc/passwd", false, false, "traversal"},
	{"../aflulit-backup-20260823-030709Z.json", false, false, "traversal before a valid name"},
	{"aflulit-backup-20260823-030709Z.json/./x", false, false, "path after a valid name"},
	{"aflulit-backup-20260823-030709Z.json\n", false, false, "trailing newline"},
	{"aflulit-backup-20260823-030709Z.json\x00", false, false, "embedded NUL"},
	{" aflulit-backup-20260823-030709Z.json", false, false, "leading space"},
}

func TestNameContract(t *testing.T) {
	for _, c := range nameContract {
		if got := Valid(c.name); got != c.valid {
			t.Errorf("Valid(%q) = %v, want %v (%s)", c.name, got, c.valid, c.reason)
		}
		if got := IsMedia(c.name); got != c.media {
			t.Errorf("IsMedia(%q) = %v, want %v (%s)", c.name, got, c.media, c.reason)
		}
		// A snapshot parses; a media bundle or a stranger never does.
		if got := Parse(c.name) != nil; got != (c.valid && !c.media) {
			t.Errorf("Parse(%q) != nil = %v, want %v (%s)", c.name, got, c.valid && !c.media, c.reason)
		}
	}
}

func TestParseFields(t *testing.T) {
	p := Parse("aflulit-backup-20260823-030709Z.json")
	if p == nil {
		t.Fatal("expected a parse")
	}
	if p.DateKey != "2026-08-23" || p.MonthKey != "2026-08" {
		t.Fatalf("bad keys: %+v", p)
	}
	if p.Time.Hour() != 3 || p.Time.Minute() != 7 || p.Time.Second() != 9 || p.Time.Location().String() != "UTC" {
		t.Fatalf("bad time: %v", p.Time)
	}
}

func TestMediaRunIdCapture(t *testing.T) {
	m := MediaFileRe.FindStringSubmatch("aflulit-media-exam-mkxy1abc-q7ztuv00.json")
	if m == nil || m[1] != "exam-mkxy1abc-q7ztuv00" {
		t.Fatalf("run id capture: %v", m)
	}
}
