package auditinspectionstalecache_test

import (
	"archiveflow/internal/audit"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditInspectionDetectsFileChangeAfterCachedRead(t *testing.T) {
	dir := t.TempDir()
	log, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.AppendAt("case-cache", "REGISTERED", 1, time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	first, err := log.Inspect("case-cache")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Errors) != 0 {
		t.Fatalf("initial inspection should be verified: %+v", first.Errors)
	}

	path := filepath.Join(dir, "audit.jsonl")
	state, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(contents, []byte(`"type":"REGISTERED"`), []byte(`"type":"TAMPERED!!"`), 1)
	if bytes.Equal(tampered, contents) {
		t.Fatal("test fixture did not locate the audit event type")
	}
	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, state.ModTime(), state.ModTime()); err != nil {
		t.Fatal(err)
	}

	second, err := log.Inspect("case-cache")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Errors) == 0 {
		t.Fatal("runtime audit inspection reused a stale verified result after audit.jsonl changed")
	}
}
