package qualityemptycaptures

import (
	"archiveflow/internal/domain"
	"testing"
)

func TestDecideQualityDoesNotPanicWhenListeningIntervalsHaveNoCapture(t *testing.T) {
	c := &domain.DigitizationCase{
		ID: "case-1", State: domain.StateCaptured, CurrentCaptureGeneration: 1,
		Plan: &domain.CapturePlan{ChannelMap: "L"},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("质量复核因缺失采集证据发生 panic: %v", r)
		}
	}()
	_ = c.DecideQuality(domain.QualityDecision{
		Generation: 1, Reviewer: "reviewer", ListeningNotes: "听检记录",
		CompletenessPassed: true, ChannelMappingPassed: true,
		ListeningIntervals: []domain.ListeningInterval{{StartMs: 0, EndMs: 1, Channel: "L", Method: "manual"}},
	})
}
