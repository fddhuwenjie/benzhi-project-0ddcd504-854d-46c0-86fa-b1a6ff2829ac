package domain

import (
	"testing"
	"time"
)

func TestFlow(t *testing.T) {
	c, _ := NewCase("1", "A", "t", "r", "tape", "all")
	if c.State != StateRegistered {
		t.Fail()
	}
	if err := c.Assess(ConditionAssessment{Assessor: "a", MoldLevel: "none", PlaybackRisk: "low", NoTreatmentRequired: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApprovePlan(CapturePlan{PlaybackDevice: "d", SignalChain: "s", TargetCodec: "wav", SampleRateHz: 48000, BitDepth: 24, ChannelMap: "L/R", Operator: "o", ApprovedBy: "p"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := c.AddCapture(CaptureGeneration{CalibrationReference: "cal", CalibrationDevice: "d", CalibratedAt: now.Add(-time.Hour), CalibrationValidUntil: now.Add(time.Hour), StartedAt: now, EndedAt: now.Add(time.Second), AssetDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AssetSizeBytes: 288000, ContainerFormat: "wav", ActualCodec: "wav", ActualSampleRateHz: 48000, ActualBitDepth: 24, ActualChannels: 2, DurationMs: 1000, PlanRevision: c.Plan.PlanRevision, PlanFingerprint: c.Plan.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	c.DecideQuality(QualityDecision{Generation: 1, CompletenessPassed: true, ChannelMappingPassed: true, Reviewer: "q", ListeningNotes: "ok", DefectMarkers: []DefectMarker{}})
	if c.State != StateQCPassed {
		t.Fail()
	}
}
