package audit_recovery_gap_test

import (
	"archiveflow/internal/audit"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditRecoveryRejectsCorruptedLogGap(t *testing.T) {
	dir := t.TempDir()
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := a.AppendAt("case-recovery", "REGISTERED", 1, at); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{broken-json}\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.AppendAt("case-recovery", "ASSESSED", 2, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	restarted, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Validate("case-recovery", 2) {
		t.Fatalf("expected corrupted audit log gap to fail validation")
	}
}

