package manifestpreviewstalecache

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"testing"
	"time"
)

func TestManifestPreviewRefreshesAfterCaseRevisionChanges(t *testing.T) {
	dataDir := t.TempDir()
	s, err := store.New(dataDir + "/store")
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dataDir + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(s, a)
	c, err := app.CreateWithRequest("create-preview-cache", "PREVIEW-001", "缓存失效复现", "馆藏授权", "tape", "全部内容")
	if err != nil {
		t.Fatal(err)
	}

	c, err = app.AssessWithRequest("assess-preview-cache", c.ID, c.Revision, domain.ConditionAssessment{
		Assessor:            "reviewer-a",
		MoldLevel:           "none",
		PlaybackRisk:        "low",
		NoTreatmentRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err = app.PlanWithRequest("plan-preview-cache", c.ID, c.Revision, domain.CapturePlan{
		PlaybackDevice: "deck-a",
		SignalChain:    "deck-a -> adc-a",
		TargetCodec:    "wav",
		SampleRateHz:   48000,
		BitDepth:       24,
		ChannelMap:     "L/R",
		Operator:       "operator-a",
		ApprovedBy:     "supervisor-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Truncate(time.Second)
	c, err = app.CaptureWithRequest("capture-preview-cache", c.ID, c.Revision, domain.CaptureGeneration{
		CalibrationReference:  "CAL-PREVIEW-1",
		CalibrationDevice:     "deck-a",
		CalibratedAt:          startedAt.Add(-time.Hour),
		CalibrationValidUntil: startedAt.Add(time.Hour),
		StartedAt:             startedAt,
		EndedAt:               startedAt.Add(time.Second),
		AssetDigest:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AssetSizeBytes:        288000,
		ContainerFormat:       "wav",
		ActualCodec:           "wav",
		ActualSampleRateHz:    48000,
		ActualBitDepth:        24,
		ActualChannels:        2,
		DurationMs:            1000,
		PlanRevision:          c.Plan.PlanRevision,
		PlanFingerprint:       c.Plan.Fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := app.PreviewManifest(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sealable || first.AuditRevision != c.Revision || first.AuditHeadDigest == "" {
		t.Fatalf("采集态预览基线异常: %#v", first)
	}

	updated, err := app.QualityWithRequest("quality-preview-cache", c.ID, c.Revision, domain.QualityDecision{
		Generation:           1,
		CompletenessPassed:   true,
		ChannelMappingPassed: true,
		Reviewer:             "reviewer-b",
		ListeningNotes:       "全程人工听检通过",
		DefectMarkers:        []domain.DefectMarker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.StateQCPassed || !a.Validate(c.ID, updated.Revision) {
		t.Fatalf("质量通过后的持久化或审计链异常: state=%s revision=%d", updated.State, updated.Revision)
	}

	second, err := app.PreviewManifest(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Sealable || second.AuditRevision != updated.Revision || second.AuditHeadDigest != a.Head(c.ID) {
		t.Fatalf("质量 revision 更新后仍返回旧预检: before=%#v after=%#v current_revision=%d", first, second, updated.Revision)
	}
}
