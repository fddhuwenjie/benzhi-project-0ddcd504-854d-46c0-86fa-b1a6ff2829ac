package auditeventsalias

import (
	"archiveflow/internal/audit"
	"testing"
	"time"
)

// TestAuditEventsMapMutationDoesNotCorruptValidation verifies that a caller
// cannot mutate the in-memory audit chain through a returned event map.
func TestAuditEventsMapMutationDoesNotCorruptValidation(t *testing.T) {
	dir := t.TempDir()
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AppendEvidenceDigestsAt("case-1", "REGISTERED", 1, "anchor", map[string]string{"source": "original"}, time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	events := a.Events("case-1")
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	events[0].EvidenceDigests["source"] = "tampered"
	if !a.Validate("case-1", 1) {
		t.Fatal("审计事件返回值被修改后，原审计链校验不应失败")
	}
}
