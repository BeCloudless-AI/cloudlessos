package securityaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLogIsBoundedPrivateAndRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	log.now = func() time.Time { return time.Unix(1234, 0) }
	log.Append(Event{
		Category: "recipe", Event: "run", Outcome: "failed", Target: "local-one",
		Detail: "Authorization: Bearer secret github_pat_fixture password=hunter2",
	})
	events, err := log.Latest(10)
	if err != nil || len(events) != 1 || events[0].Category != "recipe" {
		t.Fatalf("events = %#v, %v", events, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "security-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret", "github_pat_", "hunter2"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("audit contains secret %q: %s", secret, data)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "security-audit.jsonl")); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("unsafe audit mode: %v, %v", info, err)
	}
}
