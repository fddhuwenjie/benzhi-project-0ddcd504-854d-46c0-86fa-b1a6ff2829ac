package adjudicationmapalias

import (
	"archiveflow/internal/domain"
	"testing"
)

func TestAdjudicationResolutionMapMutationDoesNotAlterDecision(t *testing.T) {
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	c := &domain.DigitizationCase{
		ID: "case-1", State: domain.StateCaptured, CurrentCaptureGeneration: 1, Revision: 4,
		Plan: &domain.CapturePlan{Operator: "operator"},
		Quality: []domain.QualityDecision{{
			Generation: 1, QualityRevision: 3, Reviewer: "reviewer", QualityEvidenceDigest: digest,
			CountersignStatus: "DISAGREED", Decision: "PASS",
			Countersigns: []domain.QualityCountersign{{
				CountersignForRevision: 3, CountersignRevision: 4, Reviewer: "counter", Decision: "FAIL",
				Disagreements: []string{"decision"},
			}},
		}},
	}
	resolutions := map[string]string{"decision": "以主审证据为准"}
	q := domain.QualityDecision{
		Generation: 1, AdjudicationForRevision: 3, CountersignForRevision: 4,
		Adjudicator: "adjudicator", Decision: "PASS", ListeningNotes: "裁决复核记录",
		ConfirmedEvidenceDigest: digest, DisagreementResolutions: resolutions,
	}
	if err := c.DecideQuality(q); err != nil {
		t.Fatal(err)
	}
	resolutions["decision"] = "外部篡改"
	if got := c.Quality[0].Adjudication.DisagreementResolutions["decision"]; got != "以主审证据为准" {
		t.Fatalf("裁决分歧映射被外部 map 修改: %q", got)
	}
}
