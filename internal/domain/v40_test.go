package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestCustodyAndDamageEvidence(t *testing.T) {
	now := time.Now().UTC()
	receipt := &IntakeReceipt{TransferOrganization: "移交单位", Transferor: "甲", Receiver: "丙", ReceivedAt: now.Add(-2 * time.Hour), BatchNumber: "batch-1", PackagingCondition: "包装完好"}
	custody := []CustodyEvent{
		{Transferor: "甲", Receiver: "乙", OccurredAt: now.Add(-90 * time.Minute), LocationCode: "VAULT-A1", SealStatus: "SEALED", Notes: "完成首次交接"},
		{Transferor: "乙", Receiver: "丙", OccurredAt: now.Add(-60 * time.Minute), LocationCode: "VAULT-B2", SealStatus: "INTACT", Notes: "完成入库交接"},
	}
	c, err := NewCaseWithCustodyEvidence("case-custody", "CUSTODY-1", "标题", "权属", "mono tape", "内容", receipt, []CarrierFacet{{FacetID: "A", Label: "A 面", PhysicalOrder: 1, ContentScope: "内容", Playable: true}}, nil, custody)
	if err != nil {
		t.Fatal(err)
	}
	if c.CurrentCustodian != "丙" || c.CurrentLocationCode != "VAULT-B2" || !validDigest(c.CustodyChainDigest) {
		t.Fatalf("unexpected custody conclusion: %#v", c)
	}
	err = c.Assess(ConditionAssessment{Assessor: "评估员", MoldLevel: "none", Breakage: true, PlaybackRisk: "none", TreatmentEvidence: []TreatmentEvidence{{Category: "breakage", Action: "加固", PerformedBy: "修复员", CompletedAt: now.Add(-30 * time.Minute), EvidenceSummary: "已完成边缘加固"}}, DamageLocations: []DamageLocation{{FacetID: "A", Category: "breakage", PhysicalLocation: "外缘", Severity: "MAJOR", AffectedRatio: 0.25, ObservationNotes: "外缘可见断裂", EvidenceSummary: "现场观察记录完整"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Assessment.DamageSummaries) != 1 || c.Assessment.DamageSummaries[0].TotalAffectedRatio != 0.25 || !validDigest(c.Assessment.DamageLocationDigest) {
		t.Fatalf("unexpected damage summary: %#v", c.Assessment)
	}
}

func TestFileSegmentsAndOverlappingDefectImpacts(t *testing.T) {
	d1 := sha256.Sum256([]byte("segment-one"))
	d2 := sha256.Sum256([]byte("segment-two"))
	combinedHash := sha256.New()
	_, _ = combinedHash.Write(d1[:])
	_, _ = combinedHash.Write(d2[:])
	g := CaptureGeneration{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC().Add(2 * time.Second), DurationMs: 1000, AssetSizeBytes: 200, AssetDigest: hex.EncodeToString(combinedHash.Sum(nil)), FileSegments: []FileSegment{
		{Sequence: 1, SourceStartMs: 0, SourceEndMs: 600, DurationMs: 600, AssetSizeBytes: 120, AssetDigest: hex.EncodeToString(d1[:]), StartsContinuous: true, EndsContinuous: true},
		{Sequence: 2, SourceStartMs: 600, SourceEndMs: 1000, DurationMs: 400, AssetSizeBytes: 80, AssetDigest: hex.EncodeToString(d2[:]), StartsContinuous: true, EndsContinuous: true},
	}}
	if err := normalizeFileSegments(&g); err != nil {
		t.Fatal(err)
	}
	markers := []DefectMarker{
		{DefectType: "dropout", PositionMs: 100, DurationMs: 200, Channel: "L", Description: "发现一次掉样异常", Severity: "MINOR"},
		{DefectType: "dropout", PositionMs: 250, DurationMs: 200, Channel: "L", Description: "发现第二次掉样", Severity: "CRITICAL"},
	}
	_, impacts, _, digest, err := calculateDefectImpacts(markers, 1000, []string{"L"})
	if err != nil {
		t.Fatal(err)
	}
	if len(impacts) != 1 || impacts[0].AffectedDurationMs != 350 || impacts[0].MarkerCount != 2 || impacts[0].HighestSeverity != "CRITICAL" || !validDigest(digest) {
		t.Fatalf("unexpected impacts: %#v", impacts)
	}
}
