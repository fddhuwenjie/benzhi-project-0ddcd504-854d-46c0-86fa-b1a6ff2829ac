package application

import (
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type App struct {
	Store    *store.Store
	Audit    *audit.Audit
	locks    sync.Map
	createMu sync.Mutex
}

func New(s *store.Store, a *audit.Audit) *App { return &App{Store: s, Audit: a} }
func (a *App) lock(id string) func() {
	v, _ := a.locks.LoadOrStore(id, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
func (a *App) Create(accession, title, rights, carrier, scope string) (*domain.DigitizationCase, error) {
	return a.CreateWithRequest("", accession, title, rights, carrier, scope)
}
func (a *App) CreateWithRequest(requestID, accession, title, rights, carrier, scope string) (*domain.DigitizationCase, error) {
	return a.CreateWithReceiptRequest(requestID, accession, title, rights, carrier, scope, nil)
}
func (a *App) CreateWithReceiptRequest(requestID, accession, title, rights, carrier, scope string, receipt *domain.IntakeReceipt) (*domain.DigitizationCase, error) {
	return a.CreateWithEvidenceRequest(requestID, accession, title, rights, carrier, scope, receipt, nil)
}
func (a *App) CreateWithEvidenceRequest(requestID, accession, title, rights, carrier, scope string, receipt *domain.IntakeReceipt, facets []domain.CarrierFacet) (*domain.DigitizationCase, error) {
	return a.CreateWithAllEvidenceRequest(requestID, accession, title, rights, carrier, scope, receipt, facets, nil)
}
func (a *App) CreateWithAllEvidenceRequest(requestID, accession, title, rights, carrier, scope string, receipt *domain.IntakeReceipt, facets []domain.CarrierFacet, identifiers []domain.AlternativeIdentifier) (*domain.DigitizationCase, error) {
	return a.CreateWithCustodyRequest(requestID, accession, title, rights, carrier, scope, receipt, facets, identifiers, nil)
}
func (a *App) CreateWithCustodyRequest(requestID, accession, title, rights, carrier, scope string, receipt *domain.IntakeReceipt, facets []domain.CarrierFacet, identifiers []domain.AlternativeIdentifier, custody []domain.CustodyEvent) (*domain.DigitizationCase, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	payload := struct {
		Accession, Title, Rights, Carrier, Scope string
		IntakeReceipt                            *domain.IntakeReceipt          `json:"intake_receipt,omitempty"`
		CarrierFacets                            []domain.CarrierFacet          `json:"carrier_facets,omitempty"`
		AlternativeIdentifiers                   []domain.AlternativeIdentifier `json:"alternative_identifiers,omitempty"`
		CustodyEvents                            []domain.CustodyEvent          `json:"custody_events,omitempty"`
	}{accession, title, rights, carrier, scope, receipt, facets, identifiers, custody}
	key := ""
	if requestID != "" {
		key = "create:" + requestID
	}
	if c, found, err := a.idempotent(key, payload); found || err != nil {
		return c, err
	}
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	c, e := domain.NewCaseWithCustodyEvidence(fmt.Sprintf("case-%x", b), accession, title, rights, carrier, scope, receipt, facets, identifiers, custody)
	if e != nil {
		return nil, e
	}
	if existing, typ, value := a.Store.FindIdentifier(c.AccessionCode, c.AlternativeIdentifiers); existing != nil {
		return nil, domain.Conflict("馆藏标识冲突", map[string]interface{}{"case_id": existing.ID, "identifier_type": typ, "identifier_value": value})
	}
	entry, e := makeIdempotency(payload, c)
	if e != nil {
		return nil, e
	}
	if e = a.Store.Commit(c, key, entry, true); e != nil {
		return nil, e
	}
	evidenceDigest := ""
	evidenceDigests := map[string]string{}
	if c.IntakeReceipt != nil {
		evidenceDigest = c.IntakeReceipt.ReceiptDigest
	}
	if c.AlternativeIdentifierDigest != "" {
		evidenceDigests["alternative_identifiers"] = c.AlternativeIdentifierDigest
		if evidenceDigest == "" {
			evidenceDigest = c.AlternativeIdentifierDigest
		}
	}
	if c.CarrierFacetsDigest != "" {
		evidenceDigests["carrier_facets"] = c.CarrierFacetsDigest
		if evidenceDigest == "" {
			evidenceDigest = c.CarrierFacetsDigest
		}
	}
	if c.IntakeReceipt != nil {
		evidenceDigests["intake_receipt"] = c.IntakeReceipt.ReceiptDigest
	}
	if c.CustodyChainDigest != "" {
		evidenceDigests["custody_chain"] = c.CustodyChainDigest
		if evidenceDigest == "" {
			evidenceDigest = c.CustodyChainDigest
		}
	}
	if e = a.Audit.AppendEvidenceDigestsAt(c.ID, "REGISTERED", c.Revision, evidenceDigest, evidenceDigests, c.FirstAuditAt); e != nil {
		return nil, e
	}
	return c, nil
}
func (a *App) mutateWithRequest(requestID, id string, rev int64, payload interface{}, fn func(*domain.DigitizationCase) error, typ string) (*domain.DigitizationCase, error) {
	u := a.lock(id)
	defer u()
	request := struct {
		Revision int64       `json:"expected_revision"`
		Payload  interface{} `json:"payload"`
	}{rev, payload}
	key := ""
	if requestID != "" {
		key = typ + ":" + id + ":" + requestID
	}
	if c, found, err := a.idempotent(key, request); found || err != nil {
		return c, err
	}
	c, e := a.Store.Get(id)
	if e != nil {
		return nil, e
	}
	if c.Revision != rev {
		return nil, domain.ErrConflict
	}
	if e = fn(c); e != nil {
		return nil, e
	}
	entry, e := makeIdempotency(request, c)
	if e != nil {
		return nil, e
	}
	if e = a.Store.Commit(c, key, entry, false); e != nil {
		return nil, e
	}
	evidenceDigest := mutationEvidenceDigest(c, typ, payload)
	if e = a.Audit.AppendEvidenceDigests(id, typ, c.Revision, evidenceDigest, mutationEvidenceDigests(c, typ)); e != nil {
		return nil, e
	}
	return c, nil
}
func (a *App) AssessWithRequest(req, id string, rev int64, x domain.ConditionAssessment) (*domain.DigitizationCase, error) {
	if x.AssessmentVersion > 0 || strings.TrimSpace(x.CorrectionReason) != "" {
		return a.mutateWithRequest(req, id, rev, x, func(c *domain.DigitizationCase) error {
			return c.CorrectAssessment(x, x.AssessmentVersion, x.CorrectionReason)
		}, "ASSESSMENT_CORRECTED")
	}
	return a.mutateWithRequest(req, id, rev, x, func(c *domain.DigitizationCase) error { return c.Assess(x) }, "ASSESSED")
}
func (a *App) PlanWithRequest(req, id string, rev int64, x domain.CapturePlan) (*domain.DigitizationCase, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	unlock := a.lock(id)
	defer unlock()
	request := struct {
		Revision int64              `json:"expected_revision"`
		Payload  domain.CapturePlan `json:"payload"`
	}{rev, x}
	key := "PLAN:" + id + ":" + req
	if c, found, err := a.idempotent(key, request); found || err != nil {
		return c, err
	}
	c, err := a.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if c.Revision != rev {
		return nil, domain.ErrConflict
	}
	typ := "PLAN_APPROVED"
	if c.State == domain.StateReady {
		typ = "PLAN_REVISED"
		if strings.TrimSpace(x.ReapprovalReason) != "" {
			typ = "PLAN_REAPPROVED"
		}
	}
	if err = c.ApprovePlan(x); err != nil {
		return nil, err
	}
	if conflict := a.Store.PlanResourceConflict(id, *c.Plan); conflict != nil {
		return nil, domain.Conflict("采集资源预约时窗冲突", map[string]interface{}{"conflict_case_id": conflict.CaseID, "resource_type": conflict.ResourceType, "resource": conflict.Resource, "conflict_start": conflict.Start, "conflict_end": conflict.End})
	}
	entry, err := makeIdempotency(request, c)
	if err != nil {
		return nil, err
	}
	if err = a.Store.Commit(c, key, entry, false); err != nil {
		return nil, err
	}
	evidenceDigest := c.Plan.Fingerprint
	if err = a.Audit.AppendEvidenceDigests(id, typ, c.Revision, evidenceDigest, mutationEvidenceDigests(c, typ)); err != nil {
		return nil, err
	}
	return c, nil
}

func (a *App) ReleaseReservationWithRequest(req, id string, rev int64, releasedBy, reason string) (*domain.DigitizationCase, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	u := a.lock(id)
	defer u()
	payload := struct {
		Revision   int64  `json:"expected_revision"`
		Action     string `json:"action"`
		ReleasedBy string `json:"released_by"`
		Reason     string `json:"release_reason"`
	}{rev, "RELEASE", releasedBy, reason}
	key := "PLAN_RESERVATION:" + id + ":" + req
	if c, found, err := a.idempotent(key, payload); found || err != nil {
		return c, err
	}
	c, err := a.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if c.Revision != rev {
		return nil, domain.ErrConflict
	}
	if err = c.ReleasePlanReservation(releasedBy, reason); err != nil {
		return nil, err
	}
	entry, err := makeIdempotency(payload, c)
	if err != nil {
		return nil, err
	}
	if err = a.Store.Commit(c, key, entry, false); err != nil {
		return nil, err
	}
	evidence := ""
	if c.Plan != nil {
		evidence, _ = digest(struct {
			Revision int64     `json:"plan_revision"`
			Start    time.Time `json:"scheduled_start"`
			End      time.Time `json:"scheduled_end"`
			By       string    `json:"released_by"`
			Reason   string    `json:"release_reason"`
		}{c.Plan.PlanRevision, c.Plan.ScheduledStart, c.Plan.ScheduledEnd, c.Plan.ReservationReleasedBy, c.Plan.ReservationReleaseReason})
	}
	auditDetails := map[string]string{"plan_fingerprint": c.Plan.Fingerprint, "reservation_status": "RELEASED", "plan_revision": fmt.Sprintf("%d", c.Plan.PlanRevision), "scheduled_start": c.Plan.ScheduledStart.Format(time.RFC3339Nano), "scheduled_end": c.Plan.ScheduledEnd.Format(time.RFC3339Nano), "released_by": c.Plan.ReservationReleasedBy, "release_reason": c.Plan.ReservationReleaseReason}
	if err = a.Audit.AppendEvidenceDigests(id, "PLAN_RESERVATION_RELEASED", c.Revision, evidence, auditDetails); err != nil {
		return nil, err
	}
	return c, nil
}
func (a *App) CaptureWithRequest(req, id string, rev int64, x domain.CaptureGeneration) (*domain.DigitizationCase, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	request := struct {
		Revision int64                    `json:"expected_revision"`
		Payload  domain.CaptureGeneration `json:"payload"`
	}{rev, x}
	if c, found, err := a.idempotent("CAPTURED:"+id+":"+req, request); found || err != nil {
		return c, err
	}
	reference := strings.ToUpper(strings.TrimSpace(x.CalibrationReference))
	device := strings.TrimSpace(x.CalibrationDevice)
	assetDigest := strings.TrimSpace(x.AssetDigest)
	if field, conflict := a.Store.CaptureEvidenceConflict(reference, device, x.CalibratedAt.UTC(), x.CalibrationValidUntil.UTC(), assetDigest); conflict {
		return nil, domain.Conflict("采集证据与历史记录冲突", map[string]interface{}{"conflict_field": field})
	}
	for i, segment := range x.FileSegments {
		if field, conflict := a.Store.CaptureEvidenceConflict("", "", time.Time{}, time.Time{}, strings.TrimSpace(segment.AssetDigest)); conflict {
			return nil, domain.Conflict("采集分段证据与历史记录冲突", map[string]interface{}{"conflict_field": field, "segment_index": i})
		}
	}
	return a.mutateWithRequest(req, id, rev, x, func(c *domain.DigitizationCase) error { return c.AddCapture(x) }, "CAPTURED")
}
func (a *App) CaptureGroupWithRequest(req, id string, rev int64, items []domain.CaptureGeneration) (*domain.DigitizationCase, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	request := struct {
		Revision int64                      `json:"expected_revision"`
		Payload  []domain.CaptureGeneration `json:"payload"`
	}{rev, items}
	if c, found, err := a.idempotent("CAPTURED:"+id+":"+req, request); found || err != nil {
		return c, err
	}
	for i, item := range items {
		if field, conflict := a.Store.CaptureEvidenceConflict(strings.ToUpper(strings.TrimSpace(item.CalibrationReference)), strings.TrimSpace(item.CalibrationDevice), item.CalibratedAt.UTC(), item.CalibrationValidUntil.UTC(), strings.TrimSpace(item.AssetDigest)); conflict {
			return nil, domain.Conflict("采集证据与历史记录冲突", map[string]interface{}{"conflict_field": field, "item_index": i, "task_id": item.CaptureTaskID})
		}
		for segmentIndex, segment := range item.FileSegments {
			if field, conflict := a.Store.CaptureEvidenceConflict("", "", time.Time{}, time.Time{}, strings.TrimSpace(segment.AssetDigest)); conflict {
				return nil, domain.Conflict("采集分段证据与历史记录冲突", map[string]interface{}{"conflict_field": field, "item_index": i, "task_id": item.CaptureTaskID, "segment_index": segmentIndex})
			}
		}
	}
	return a.mutateWithRequest(req, id, rev, items, func(c *domain.DigitizationCase) error { return c.AddCaptureGroup(items) }, "CAPTURED")
}
func (a *App) QualityWithRequest(req, id string, rev int64, x domain.QualityDecision) (*domain.DigitizationCase, error) {
	typ := "QUALITY"
	if x.AdjudicationForRevision != 0 {
		typ = "QUALITY_ADJUDICATED"
	}
	return a.mutateWithRequest(req, id, rev, x, func(c *domain.DigitizationCase) error { return c.DecideQuality(x) }, typ)
}

func (a *App) CustodyWithRequest(req, id string, rev int64, event domain.CustodyEvent) (*domain.DigitizationCase, error) {
	return a.mutateWithRequest(req, id, rev, event, func(c *domain.DigitizationCase) error { return c.AppendCustodyTransfer(event) }, "CUSTODY_TRANSFER")
}
func (a *App) RecaptureWithRequest(req, id string, rev int64, x domain.RecaptureAction) (*domain.DigitizationCase, error) {
	typ := "RECAPTURE_AUTHORIZED"
	switch strings.ToLower(strings.TrimSpace(x.Action)) {
	case "revoke":
		typ = "RECAPTURE_REVOKED"
	case "renew":
		typ = "RECAPTURE_RENEWED"
	}
	return a.mutateWithRequest(req, id, rev, x, func(c *domain.DigitizationCase) error { return c.AuthorizeRecapture(x) }, typ)
}
func (a *App) SealWithRequest(req, id string, rev int64, by string) (*domain.DigitizationCase, error) {
	return a.mutateWithRequest(req, id, rev, by, func(c *domain.DigitizationCase) error {
		if !a.Audit.Validate(id, c.Revision) {
			return domain.ErrIntegrity
		}
		m, e := c.BuildManifest(a.Audit.Head(id), by)
		if e != nil {
			return e
		}
		return c.Seal(m)
	}, "SEALED")
}

type idempotencyEntry struct {
	RequestDigest string                  `json:"request_digest"`
	Case          domain.DigitizationCase `json:"case"`
}

func digest(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func makeIdempotency(request interface{}, c *domain.DigitizationCase) ([]byte, error) {
	d, err := digest(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(idempotencyEntry{RequestDigest: d, Case: *c})
}

func (a *App) idempotent(key string, request interface{}) (*domain.DigitizationCase, bool, error) {
	if key == "" || strings.HasSuffix(key, ":") {
		return nil, false, nil
	}
	b, ok := a.Store.GetIdempotency(key)
	if !ok {
		return nil, false, nil
	}
	var saved idempotencyEntry
	if json.Unmarshal(b, &saved) != nil {
		return nil, false, domain.ErrIntegrity
	}
	return &saved.Case, true, nil
}
func (a *App) mutate(id string, rev int64, fn func(*domain.DigitizationCase) error, typ string) (*domain.DigitizationCase, error) {
	u := a.lock(id)
	defer u()
	return a.mutateLocked(id, rev, fn, typ)
}
func (a *App) mutateLocked(id string, rev int64, fn func(*domain.DigitizationCase) error, typ string) (*domain.DigitizationCase, error) {
	c, e := a.Store.Get(id)
	if e != nil {
		return nil, e
	}
	if c.Revision != rev {
		return nil, domain.ErrConflict
	}
	if e = fn(c); e != nil {
		return nil, e
	}
	if e = a.Store.Put(c); e != nil {
		return nil, e
	}
	if e = a.Audit.AppendEvidenceDigests(id, typ, c.Revision, mutationEvidenceDigest(c, typ, nil), mutationEvidenceDigests(c, typ)); e != nil {
		return nil, e
	}
	return c, nil
}

func mutationEvidenceDigest(c *domain.DigitizationCase, typ string, fallback interface{}) string {
	switch typ {
	case "CUSTODY_TRANSFER":
		return c.CustodyChainDigest
	case "ASSESSED", "ASSESSMENT_CORRECTED":
		if c.Assessment != nil {
			if c.Assessment.ObservationEvidenceDigest != "" {
				return c.Assessment.ObservationEvidenceDigest
			}
			if c.Assessment.Acclimatization != nil && c.Assessment.Acclimatization.Digest != "" {
				return c.Assessment.Acclimatization.Digest
			}
			if c.Assessment.DamageLocationDigest != "" {
				return c.Assessment.DamageLocationDigest
			}
			value, _ := digest(c.Assessment.TreatmentEvidence)
			return value
		}
	case "PLAN_APPROVED", "PLAN_REVISED", "PLAN_REAPPROVED":
		if c.Plan != nil {
			return c.Plan.Fingerprint
		}
	case "PLAN_RESERVATION_RELEASED":
		if c.Plan != nil {
			return c.Plan.Fingerprint
		}
	case "CAPTURED":
		if len(c.Captures) > 0 {
			digests := []string{}
			for i := len(c.Captures) - 1; i >= 0 && c.Captures[i].Generation == c.CurrentCaptureGeneration; i-- {
				digests = append(digests, c.Captures[i].TechnicalEvidenceDigest)
			}
			value, _ := digest(digests)
			return value
		}
	case "QUALITY", "QUALITY_ADJUDICATED":
		if len(c.Quality) > 0 {
			if c.Quality[len(c.Quality)-1].QualityEvidenceDigest != "" {
				return c.Quality[len(c.Quality)-1].QualityEvidenceDigest
			}
			if c.Quality[len(c.Quality)-1].RemediationEffectDigest != "" {
				return c.Quality[len(c.Quality)-1].RemediationEffectDigest
			}
			if c.Quality[len(c.Quality)-1].ListeningCoverageDigest != "" {
				return c.Quality[len(c.Quality)-1].ListeningCoverageDigest
			}
			return c.Quality[len(c.Quality)-1].DefectSummary
		}
	case "RECAPTURE_AUTHORIZED", "RECAPTURE_REVOKED", "RECAPTURE_RENEWED":
		if len(c.Recaptures) > 0 {
			value, _ := digest(c.Recaptures[len(c.Recaptures)-1].Remediations)
			return value
		}
	case "SEALED":
		if c.Manifest != nil {
			return c.Manifest.CanonicalPayloadDigest
		}
	}
	value, _ := digest(fallback)
	return value
}

func mutationEvidenceDigests(c *domain.DigitizationCase, typ string) map[string]string {
	result := map[string]string{}
	switch typ {
	case "CUSTODY_TRANSFER":
		if c.CustodyChainDigest != "" {
			result["custody_chain"] = c.CustodyChainDigest
		}
	case "ASSESSED", "ASSESSMENT_CORRECTED":
		if c.Assessment != nil {
			if typ == "ASSESSMENT_CORRECTED" {
				current, _ := digest(c.Assessment)
				result["assessment_after"] = current
				if len(c.AssessmentHistory) > 0 {
					previous, _ := digest(c.AssessmentHistory[len(c.AssessmentHistory)-1])
					result["assessment_before"] = previous
				}
			}
			if c.Assessment.ObservationEvidenceDigest != "" {
				result["observation_evidence"] = c.Assessment.ObservationEvidenceDigest
			}
			if c.Assessment.Acclimatization != nil && c.Assessment.Acclimatization.Digest != "" {
				result["acclimatization"] = c.Assessment.Acclimatization.Digest
			}
			if c.Assessment.DamageLocationDigest != "" {
				result["damage_locations"] = c.Assessment.DamageLocationDigest
			}
		}
	case "PLAN_APPROVED", "PLAN_REVISED", "PLAN_REAPPROVED":
		if c.Plan != nil {
			result["plan_fingerprint"] = c.Plan.Fingerprint
			if c.Plan.RiskControlDigest != "" {
				result["risk_controls"] = c.Plan.RiskControlDigest
			}
			if c.Plan.TaskCoverageDigest != "" {
				result["capture_task_coverage"] = c.Plan.TaskCoverageDigest
			}
			if !c.Plan.ScheduledStart.IsZero() {
				value, _ := digest(struct {
					Start time.Time `json:"scheduled_start"`
					End   time.Time `json:"scheduled_end"`
				}{c.Plan.ScheduledStart, c.Plan.ScheduledEnd})
				result["resource_window"] = value
			}
		}
	case "CAPTURED":
		if len(c.Captures) > 0 {
			mapping := map[string]string{}
			for _, capture := range c.Captures {
				if capture.Generation != c.CurrentCaptureGeneration {
					continue
				}
				mapping[capture.CaptureTaskID] = capture.AssetDigest
				result["technical_evidence:"+capture.CaptureTaskID] = capture.TechnicalEvidenceDigest
				if capture.OperationTimelineDigest != "" {
					result["operation_timeline:"+capture.CaptureTaskID] = capture.OperationTimelineDigest
				}
				if capture.FixityDigest != "" {
					result["chunk_fixity:"+capture.CaptureTaskID] = capture.FixityDigest
				}
				if capture.CalibrationEvidenceDigest != "" {
					result["calibration_evidence:"+capture.CaptureTaskID] = capture.CalibrationEvidenceDigest
				}
				if len(capture.FileSegments) > 0 {
					value, _ := digest(capture.FileSegments)
					result["file_segments:"+capture.CaptureTaskID] = value
				}
			}
			value, _ := digest(mapping)
			result["task_asset_mapping"] = value
			last := c.Captures[len(c.Captures)-1]
			result["technical_evidence"] = last.TechnicalEvidenceDigest
			if last.OperationTimelineDigest != "" {
				result["operation_timeline"] = last.OperationTimelineDigest
			}
			if last.FixityDigest != "" {
				result["chunk_fixity"] = last.FixityDigest
			}
			if last.CalibrationEvidenceDigest != "" {
				result["calibration_evidence"] = last.CalibrationEvidenceDigest
			}
		}
	case "QUALITY", "QUALITY_ADJUDICATED":
		if len(c.Quality) > 0 {
			quality := c.Quality[len(c.Quality)-1]
			result["defect_markers"] = quality.DefectSummary
			if quality.ListeningCoverageDigest != "" {
				result["listening_coverage"] = quality.ListeningCoverageDigest
			}
			if quality.RemediationEffectDigest != "" {
				result["remediation_effect"] = quality.RemediationEffectDigest
			}
			if quality.QualityEvidenceDigest != "" {
				result["quality_evidence"] = quality.QualityEvidenceDigest
			}
			if quality.DefectImpactDigest != "" {
				result["defect_impacts"] = quality.DefectImpactDigest
			}
			if quality.Adjudication != nil {
				value, _ := digest(quality.Adjudication)
				result["quality_adjudication"] = value
			}
			if len(quality.Countersigns) > 0 {
				value, _ := digest(quality.Countersigns[len(quality.Countersigns)-1])
				result["quality_countersign"] = value
			}
		}
	case "RECAPTURE_AUTHORIZED", "RECAPTURE_REVOKED", "RECAPTURE_RENEWED":
		if len(c.Recaptures) > 0 {
			value, _ := digest(c.Recaptures[len(c.Recaptures)-1].Remediations)
			result["remediations"] = value
			if c.Recaptures[len(c.Recaptures)-1].RemediationEvidenceDigest != "" {
				result["remediation_evidence"] = c.Recaptures[len(c.Recaptures)-1].RemediationEvidenceDigest
			}
			if c.Recaptures[len(c.Recaptures)-1].Escalation != nil {
				escalation, _ := digest(c.Recaptures[len(c.Recaptures)-1].Escalation)
				result["escalation"] = escalation
			}
		}
	case "SEALED":
		if c.Manifest != nil {
			result["canonical_payload"] = c.Manifest.CanonicalPayloadDigest
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
func (a *App) Assess(id string, rev int64, x domain.ConditionAssessment) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error { return c.Assess(x) }, "ASSESSED")
}
func (a *App) Plan(id string, rev int64, x domain.CapturePlan) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error { return c.ApprovePlan(x) }, "PLAN_APPROVED")
}
func (a *App) Capture(id string, rev int64, x domain.CaptureGeneration) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error { return c.AddCapture(x) }, "CAPTURED")
}
func (a *App) Quality(id string, rev int64, x domain.QualityDecision) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error { return c.DecideQuality(x) }, "QUALITY")
}
func (a *App) Recapture(id string, rev int64, x domain.RecaptureAction) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error { return c.AuthorizeRecapture(x) }, "RECAPTURE_AUTHORIZED")
}
func (a *App) Verify(id string) (*domain.PreservationManifest, string, bool, error) {
	manifest, head, reasons, e := a.VerifyDetails(id)
	return manifest, head, len(reasons) == 0, e
}

func (a *App) VerifyDetails(id string) (*domain.PreservationManifest, string, []string, error) {
	c, e := a.Store.Get(id)
	if e != nil {
		return nil, "", nil, e
	}
	head := a.Audit.Head(id)
	reasons := []string{}
	if c.State != domain.StateSealed || c.Manifest == nil {
		reasons = append(reasons, "保存包清单不可用")
		return c.Manifest, head, reasons, nil
	}
	if !c.VerifyManifest() {
		reasons = append(reasons, "保存包清单不匹配")
	}
	if !a.Audit.Validate(id, c.Revision) {
		reasons = append(reasons, "审计链不匹配")
	}
	if c.Revision <= 1 || c.Manifest.AuditRevision != c.Revision-1 || c.Manifest.AuditHeadDigest != a.Audit.HeadAt(id, int(c.Revision-1)) {
		reasons = append(reasons, "审计头不匹配")
	}
	return c.Manifest, head, reasons, nil
}

func (a *App) ManifestVerificationDetails(id string) (*domain.PreservationManifest, string, domain.ManifestVerification, []string, error) {
	unlock := a.lock(id)
	defer unlock()
	c, err := a.Store.Get(id)
	if err != nil {
		return nil, "", domain.ManifestVerification{}, nil, err
	}
	head := a.Audit.Head(id)
	verification := c.ManifestVerification()
	reasons := []string{}
	if c.State != domain.StateSealed || c.Manifest == nil {
		reasons = append(reasons, "保存包清单不可用")
		return c.Manifest, head, verification, reasons, nil
	}
	if !verification.Valid || !c.VerifyManifest() {
		reasons = append(reasons, "保存包清单不匹配")
	}
	if !a.Audit.Validate(id, c.Revision) {
		reasons = append(reasons, "审计链不匹配")
	}
	if c.Revision <= 1 || c.Manifest.AuditRevision != c.Revision-1 || c.Manifest.AuditHeadDigest != a.Audit.HeadAt(id, int(c.Revision-1)) {
		reasons = append(reasons, "审计头不匹配")
	}
	verification.Valid = len(reasons) == 0
	if verification.Valid {
		verification.Status = "VERIFIED"
	} else {
		verification.Status = "INVALID"
	}
	return c.Manifest, head, verification, reasons, nil
}

func (a *App) AuditPage(id string, afterRevision int64, limit int) (domain.AuditPage, error) {
	return a.AuditSearch(id, domain.AuditQuery{AfterRevision: afterRevision, Limit: limit})
}
func (a *App) Seal(id string, rev int64, by string) (*domain.DigitizationCase, error) {
	return a.mutate(id, rev, func(c *domain.DigitizationCase) error {
		if !a.Audit.Validate(id, c.Revision) {
			return domain.ErrIntegrity
		}
		m, e := c.BuildManifest(a.Audit.Head(id), by)
		if e != nil {
			return e
		}
		return c.Seal(m)
	}, "SEALED")
}
