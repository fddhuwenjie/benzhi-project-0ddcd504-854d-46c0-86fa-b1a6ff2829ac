package audit_append_rotation_test

import (
	"archiveflow/internal/audit"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAppenderReopensRotatedLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	rotatedPath := filepath.Join(dir, "audit.jsonl.1")
	journal, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(logPath, rotatedPath); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(logPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	if err = journal.AppendAt("case-rotation", "REGISTERED", 1, at); err != nil {
		t.Fatal(err)
	}
	if !journal.Validate("case-rotation", 1) {
		t.Fatal("文件替换后进程内审计链应包含首个事件")
	}

	restarted, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := restarted.Events("case-rotation")
	if len(events) != 1 || !restarted.Validate("case-rotation", 1) {
		t.Fatalf("文件替换后的首个事件未写入当前 audit.jsonl: events=%d valid=%t", len(events), restarted.Validate("case-rotation", 1))
	}
}
