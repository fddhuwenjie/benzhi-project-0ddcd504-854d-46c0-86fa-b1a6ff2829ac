package audit_retry_evidence_loss_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditRetryPreservesOriginalRegistrationEvidence(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(s, a)
	auditPath := filepath.Join(dir, "audit.jsonl")
	if err := os.Mkdir(auditPath, 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := &domain.IntakeReceipt{
		TransferOrganization: "移交单位",
		Transferor:           "移交员甲",
		Receiver:             "接收员乙",
		ReceivedAt:           time.Now().UTC().Add(-time.Hour),
		BatchNumber:          "batch-retry",
		PackagingCondition:   "包装与封签完好",
	}
	const requestID = "registration-audit-retry"
	_, err = app.CreateWithReceiptRequest(requestID, "RETRY-001", "重试证据测试", "权属清晰", "open reel", "完整内容", receipt)
	if err == nil {
		t.Fatal("审计资源失效时首次建档应返回错误")
	}
	if s.Count() != 1 {
		t.Fatalf("首次失败后应存在已提交的幂等快照，实际个案数 %d", s.Count())
	}
	if err := os.Remove(auditPath); err != nil {
		t.Fatal(err)
	}
	retried, err := app.CreateWithReceiptRequest(requestID, "RETRY-001", "重试证据测试", "权属清晰", "open reel", "完整内容", receipt)
	if err != nil {
		t.Fatalf("资源恢复后的同键重试失败: %v", err)
	}
	events := a.Events(retried.ID)
	if len(events) != 1 || !a.Validate(retried.ID, retried.Revision) {
		t.Fatalf("重试后审计链未恢复为单个可验证事件: %#v", events)
	}
	registered := events[0]
	if registered.At != retried.FirstAuditAt {
		t.Fatalf("重试合成事件未保留原始登记时间: got %s want %s", registered.At, retried.FirstAuditAt)
	}
	if registered.EvidenceDigest != retried.IntakeReceipt.ReceiptDigest || registered.EvidenceDigests["intake_receipt"] != retried.IntakeReceipt.ReceiptDigest {
		t.Fatalf("重试合成事件丢失原始接收凭证摘要: event=%#v receipt=%s", registered, retried.IntakeReceipt.ReceiptDigest)
	}
}
