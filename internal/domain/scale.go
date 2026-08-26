package domain

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

var alternativeIdentifierTypes = map[string]string{
	"LEGACY_ACCESSION": "LEGACY_ACCESSION", "LEGACY_ACCESSION_CODE": "LEGACY_ACCESSION", "OLD_ACCESSION": "LEGACY_ACCESSION",
	"SHELFMARK": "SHELFMARK", "SHELF_MARK": "SHELFMARK", "SHELF_LOCATION": "SHELFMARK",
	"CARRIER_BARCODE": "CARRIER_BARCODE", "BARCODE": "CARRIER_BARCODE",
}

func NormalizeAlternativeIdentifiers(items []AlternativeIdentifier) ([]AlternativeIdentifier, string, error) {
	normalized := make([]AlternativeIdentifier, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		t := strings.ToUpper(strings.TrimSpace(item.Type))
		t = strings.ReplaceAll(t, "-", "_")
		canonical, ok := alternativeIdentifierTypes[t]
		value := strings.ToUpper(strings.Join(strings.Fields(item.Value), " "))
		if !ok || value == "" {
			return nil, "", Invalid("替代标识无效", map[string]interface{}{"item_index": i, "field": "alternative_identifiers", "identifier_type": t})
		}
		key := value
		if seen[key] {
			return nil, "", Invalid("同一个案替代标识重复", map[string]interface{}{"item_index": i, "identifier_type": canonical, "identifier_value": value})
		}
		seen[key] = true
		normalized[i] = AlternativeIdentifier{Type: canonical, Value: value}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Type == normalized[j].Type {
			return normalized[i].Value < normalized[j].Value
		}
		return normalized[i].Type < normalized[j].Type
	})
	return normalized, stableDigest(normalized), nil
}

func NewCaseWithAllEvidence(id, accession, title, rights, carrier, scope string, intake *IntakeReceipt, facets []CarrierFacet, identifiers []AlternativeIdentifier) (*DigitizationCase, error) {
	c, err := NewCaseWithEvidence(id, accession, title, rights, carrier, scope, intake, facets)
	if err != nil {
		return nil, err
	}
	normalized, digest, err := NormalizeAlternativeIdentifiers(identifiers)
	if err != nil {
		return nil, err
	}
	for i, identifier := range normalized {
		if identifier.Value == c.AccessionCode {
			return nil, Invalid("替代标识不得与主馆藏号重复", map[string]interface{}{"item_index": i, "identifier_value": identifier.Value})
		}
	}
	c.AlternativeIdentifiers = normalized
	c.AlternativeIdentifierDigest = digest
	c.AssessmentHistory = []ConditionAssessment{}
	return c, nil
}

func (c *DigitizationCase) CorrectAssessment(a ConditionAssessment, version int, reason string) error {
	if c.State != StateAssessed || c.Plan != nil || len(c.Captures) != 0 || c.Manifest != nil {
		return Conflict("当前状态不允许更正评估", map[string]interface{}{"current_state": c.State, "allowed_states": []State{StateAssessed}})
	}
	if c.Assessment == nil || version != c.Assessment.AssessmentVersion {
		current := 0
		if c.Assessment != nil {
			current = c.Assessment.AssessmentVersion
		}
		return Conflict("被更正的评估版本不是当前版本", map[string]interface{}{"current_assessment_version": current})
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Invalid("更正原因不能为空", map[string]interface{}{"field": "correction_reason"})
	}
	clone := *c
	clone.State, clone.Assessment = StateRegistered, nil
	if err := clone.Assess(a); err != nil {
		return err
	}
	now := time.Now().UTC()
	previous := *c.Assessment
	previous.CorrectionReason, previous.CorrectedBy, previous.CorrectedAt = reason, a.Assessor, &now
	c.AssessmentHistory = append(c.AssessmentHistory, previous)
	clone.Assessment.AssessmentVersion = version + 1
	clone.Assessment.CorrectionReason = reason
	clone.Assessment.CorrectedBy = clone.Assessment.Assessor
	clone.Assessment.CorrectedAt = &now
	c.Assessment = clone.Assessment
	c.Revision++
	return nil
}

func (p *CapturePlan) RefreshValidity(now time.Time) {
	if p.ValidUntil.IsZero() || now.After(p.ValidUntil) {
		p.ValidityStatus = "EXPIRED"
	} else {
		p.ValidityStatus = "ACTIVE"
	}
	if p.ReservationStatus != "CONSUMED" && p.ReservationStatus != "RELEASED" && !p.ScheduledEnd.IsZero() {
		if now.After(p.ScheduledEnd) || now.After(p.ValidUntil) {
			p.ReservationStatus = "EXPIRED"
		} else {
			p.ReservationStatus = "ACTIVE"
		}
	}
}

func normalizeCalibrationEvidence(g *CaptureGeneration, channels []string) error {
	if g.CalibrationMeasurements == nil {
		return nil // 兼容历史单文件凭证；新测量一旦提交即执行完整门禁。
	}
	profile := g.CalibrationProfile
	if profile == nil {
		return Invalid("校准测量必须提交 calibration_profile", map[string]interface{}{"field": "calibration_profile"})
	}
	if profile.ReferenceFrequencyHz <= 0 || !finite(profile.ReferenceFrequencyHz) {
		return Invalid("校准参考频率无效", map[string]interface{}{"field": "calibration_profile.reference_frequency_hz"})
	}
	if profile.LevelToleranceDB == 0 {
		profile.LevelToleranceDB = 1.0
	}
	if profile.FrequencyToleranceHz == 0 {
		profile.FrequencyToleranceHz = 2.0
	}
	if profile.ChannelDifferenceDB == 0 {
		profile.ChannelDifferenceDB = 0.5
	}
	if profile.LevelToleranceDB <= 0 || profile.FrequencyToleranceHz <= 0 || profile.ChannelDifferenceDB <= 0 {
		return Invalid("校准门限无效", map[string]interface{}{"field": "calibration_profile"})
	}
	want, seen := map[string]bool{}, map[string]bool{}
	for _, channel := range channels {
		want[strings.ToUpper(channel)] = true
	}
	measurements := append([]CalibrationMeasurement(nil), g.CalibrationMeasurements...)
	levels := []float64{}
	results := make([]CalibrationResult, 0, len(measurements))
	for i := range measurements {
		m := &measurements[i]
		m.Channel = strings.ToUpper(strings.TrimSpace(m.Channel))
		m.InstrumentID = strings.TrimSpace(m.InstrumentID)
		m.InstrumentCertificateDigest = strings.ToLower(strings.TrimSpace(m.InstrumentCertificateDigest))
		if m.MeasuredFrequencyHz == 0 {
			m.MeasuredFrequencyHz = m.ReferenceFrequencyHz
		}
		if !want[m.Channel] || seen[m.Channel] || !finite(m.ReferenceFrequencyHz) || !finite(m.MeasuredFrequencyHz) || !finite(m.TargetLevelDBFS) || !finite(m.MeasuredLevelDBFS) || !validDigest(m.InstrumentCertificateDigest) || m.InstrumentID == "" || m.MeasuredAt.Before(g.CalibratedAt) || m.MeasuredAt.After(g.CalibrationValidUntil) || !m.MeasuredAt.Before(g.StartedAt) {
			return Invalid("校准测量无效", map[string]interface{}{"field": "calibration_measurements", "item_index": i, "channel": m.Channel})
		}
		if math.Abs(m.ReferenceFrequencyHz-profile.ReferenceFrequencyHz) > 0.000001 {
			return Invalid("测量参考频率与策略不一致", map[string]interface{}{"item_index": i})
		}
		seen[m.Channel] = true
		levels = append(levels, m.MeasuredLevelDBFS)
		results = append(results, CalibrationResult{Channel: m.Channel, LevelDeviationDB: math.Abs(m.MeasuredLevelDBFS - m.TargetLevelDBFS), FrequencyDeviationHz: math.Abs(m.MeasuredFrequencyHz - m.ReferenceFrequencyHz)})
	}
	missing := []string{}
	for _, channel := range channels {
		if !seen[strings.ToUpper(channel)] {
			missing = append(missing, strings.ToUpper(channel))
		}
	}
	if len(missing) != 0 || len(seen) != len(want) {
		return Invalid("校准测量未覆盖全部方案声道", map[string]interface{}{"field": "calibration_measurements", "missing_channels": missing})
	}
	channelDifference := 0.0
	if len(levels) > 1 {
		min, max := levels[0], levels[0]
		for _, v := range levels[1:] {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		channelDifference = max - min
	}
	failed := []CalibrationResult{}
	for i := range results {
		results[i].ChannelDifferenceDB = channelDifference
		results[i].Passed = results[i].LevelDeviationDB <= profile.LevelToleranceDB && results[i].FrequencyDeviationHz <= profile.FrequencyToleranceHz && channelDifference <= profile.ChannelDifferenceDB
		if !results[i].Passed {
			failed = append(failed, results[i])
		}
	}
	if len(failed) != 0 {
		return Invalid("校准测量未通过门限", map[string]interface{}{"field": "calibration_measurements", "calculation_results": results})
	}
	sort.Slice(measurements, func(i, j int) bool { return measurements[i].Channel < measurements[j].Channel })
	sort.Slice(results, func(i, j int) bool { return results[i].Channel < results[j].Channel })
	g.CalibrationProfile, g.CalibrationMeasurements, g.CalibrationResults = profile, measurements, results
	g.CalibrationPolicyVersion, g.CalibrationStatus = "capture-calibration-2026.1", "PASS"
	g.CalibrationEvidenceDigest = stableDigest(struct {
		Profile      *CalibrationProfile      `json:"profile"`
		Measurements []CalibrationMeasurement `json:"measurements"`
		Results      []CalibrationResult      `json:"results"`
		Policy       string                   `json:"policy_version"`
	}{profile, measurements, results, g.CalibrationPolicyVersion})
	return nil
}

func cloneCaseValue(c *DigitizationCase) DigitizationCase {
	b, _ := json.Marshal(c)
	var clone DigitizationCase
	_ = json.Unmarshal(b, &clone)
	return clone
}

func (c *DigitizationCase) AddCaptureGroup(items []CaptureGeneration) error {
	if len(items) == 0 {
		return Invalid("成组采集至少包含一项", map[string]interface{}{"field": "items"})
	}
	if c.State != StateReady || c.Plan == nil {
		return ErrState
	}
	if strings.EqualFold(c.Plan.ReservationStatus, "RELEASED") {
		return Conflict("当前方案预约已释放，必须重新排期", map[string]interface{}{"reservation_status": "RELEASED", "plan_revision": c.Plan.PlanRevision})
	}
	want := map[string]int{}
	for _, task := range c.Plan.CaptureTasks {
		want[task.TaskID] = task.ExecutionOrder
	}
	seenTask, seenDigest := map[string]bool{}, map[string]bool{}
	seenSegmentDigest := map[string]bool{}
	normalized := make([]CaptureGeneration, len(items))
	var authorizationCopy []RecaptureAction
	for i, item := range items {
		item.CaptureTaskID = strings.ToUpper(strings.TrimSpace(item.CaptureTaskID))
		if seenTask[item.CaptureTaskID] || want[item.CaptureTaskID] == 0 {
			return Invalid("成组采集任务重复或不属于当前方案", map[string]interface{}{"item_index": i, "task_id": item.CaptureTaskID})
		}
		digest := strings.ToLower(strings.TrimSpace(item.AssetDigest))
		if seenDigest[digest] {
			return Invalid("成组采集资产摘要重复", map[string]interface{}{"item_index": i, "task_id": item.CaptureTaskID, "field": "asset_digest"})
		}
		seenTask[item.CaptureTaskID], seenDigest[digest] = true, true
		for segmentIndex, segment := range item.FileSegments {
			segmentDigest := strings.ToLower(strings.TrimSpace(segment.AssetDigest))
			if seenSegmentDigest[segmentDigest] {
				return Invalid("成组采集文件分段摘要重复", map[string]interface{}{"item_index": i, "task_id": item.CaptureTaskID, "segment_index": segmentIndex, "field": "asset_digest"})
			}
			seenSegmentDigest[segmentDigest] = true
		}
		clone := cloneCaseValue(c)
		for _, task := range clone.Plan.CaptureTasks {
			if task.TaskID == item.CaptureTaskID {
				clone.Plan.CaptureTasks = []CaptureTask{task}
				break
			}
		}
		if err := clone.AddCapture(item); err != nil {
			if detail, ok := err.(*DetailError); ok {
				if detail.Details == nil {
					detail.Details = map[string]interface{}{}
				}
				detail.Details["item_index"] = i
				detail.Details["task_id"] = item.CaptureTaskID
			}
			return err
		}
		normalized[i] = clone.Captures[len(clone.Captures)-1]
		if i == 0 {
			authorizationCopy = clone.Recaptures
		}
	}
	missing := []string{}
	for task := range want {
		if !seenTask[task] {
			missing = append(missing, task)
		}
	}
	if len(missing) != 0 || len(seenTask) != len(want) {
		sort.Strings(missing)
		return Invalid("成组采集未准确覆盖当前方案", map[string]interface{}{"missing_tasks": missing})
	}
	sort.Slice(normalized, func(i, j int) bool { return want[normalized[i].CaptureTaskID] < want[normalized[j].CaptureTaskID] })
	c.Captures = append(c.Captures, normalized...)
	if len(normalized) > 0 {
		c.consumePlanReservation(normalized[len(normalized)-1].EndedAt)
	}
	c.CurrentCaptureGeneration++
	if authorizationCopy != nil {
		c.Recaptures = authorizationCopy
	}
	c.State = StateCaptured
	c.Revision++
	return nil
}

func (c *DigitizationCase) currentEscalation(generation int) *RecaptureEscalation {
	for i := len(c.Recaptures) - 1; i >= 0; i-- {
		if c.Recaptures[i].Generation == generation && c.Recaptures[i].Escalation != nil {
			return c.Recaptures[i].Escalation
		}
	}
	return nil
}

func (c *DigitizationCase) EscalationStatus() RecaptureEscalation {
	if current := c.currentEscalation(c.CurrentCaptureGeneration + 1); current != nil {
		return *current
	}
	return c.EscalationRequirement()
}

func (c *DigitizationCase) applyAdjudication(q QualityDecision) error {
	if q.Generation != c.CurrentCaptureGeneration {
		return Invalid("裁决代次不是当前采集代次", map[string]interface{}{"current_generation": c.CurrentCaptureGeneration})
	}
	var primary *QualityDecision
	for i := len(c.Quality) - 1; i >= 0; i-- {
		candidate := &c.Quality[i]
		if candidate.QualityRevision == q.AdjudicationForRevision && candidate.Generation == c.CurrentCaptureGeneration {
			primary = candidate
			break
		}
	}
	if primary == nil || primary.CountersignStatus != "DISAGREED" || len(primary.Countersigns) == 0 || primary.Adjudication != nil {
		return Conflict("当前质量分歧不可裁决", map[string]interface{}{"adjudication_for_revision": q.AdjudicationForRevision})
	}
	countersign := primary.Countersigns[len(primary.Countersigns)-1]
	if q.CountersignForRevision != countersign.CountersignRevision && q.CountersignForRevision != countersign.CountersignForRevision {
		return Conflict("裁决引用的会签 revision 已变化", map[string]interface{}{"current_countersign_for_revision": countersign.CountersignForRevision})
	}
	by := strings.TrimSpace(q.Adjudicator)
	if by == "" {
		by = strings.TrimSpace(q.Reviewer)
	}
	decision, notes := strings.ToUpper(strings.TrimSpace(q.Decision)), strings.TrimSpace(q.ListeningNotes)
	digest := strings.ToLower(strings.TrimSpace(q.ConfirmedEvidenceDigest))
	if by == "" || (decision != "PASS" && decision != "FAIL") || !validSummary(notes) || digest != primary.QualityEvidenceDigest {
		return Invalid("质量裁决材料无效", map[string]interface{}{"expected_evidence_digest": primary.QualityEvidenceDigest})
	}
	prohibited := []string{primary.Reviewer, countersign.Reviewer}
	if c.Plan != nil {
		prohibited = append(prohibited, c.Plan.Operator)
	}
	for _, person := range prohibited {
		if strings.EqualFold(person, by) {
			return Conflict("裁决人不满足人员分离要求", map[string]interface{}{"adjudicator": by})
		}
	}
	for _, item := range countersign.Disagreements {
		if strings.TrimSpace(q.DisagreementResolutions[item]) == "" {
			return Invalid("裁决未覆盖全部分歧项", map[string]interface{}{"missing_disagreement": item})
		}
	}
	allowedDisagreements := map[string]bool{}
	for _, item := range countersign.Disagreements {
		allowedDisagreements[item] = true
	}
	for item := range q.DisagreementResolutions {
		if !allowedDisagreements[item] {
			return Invalid("裁决包含未声明的分歧项", map[string]interface{}{"disagreement": item})
		}
	}
	now := time.Now().UTC()
	failures := []string{}
	if decision == "FAIL" {
		for item := range q.DisagreementResolutions {
			failures = append(failures, strings.ToLower(strings.TrimSpace(item)))
		}
		sort.Strings(failures)
	}
	primary.Adjudication = &QualityAdjudication{AdjudicationForRevision: q.AdjudicationForRevision, CountersignForRevision: q.CountersignForRevision, Adjudicator: by, Decision: decision, ListeningNotes: notes, ConfirmedEvidenceDigest: digest, DisagreementResolutions: q.DisagreementResolutions, AdjudicatedAt: now, FailureCategories: failures}
	primary.CountersignStatus = "ADJUDICATED"
	if decision == "PASS" {
		c.State = StateQCPassed
	} else {
		c.State = StateRecapture
	}
	c.Revision++
	return nil
}

func (c *DigitizationCase) EscalationRequirement() RecaptureEscalation {
	result := RecaptureEscalation{TriggeredCategories: []string{}, FailureGenerations: []int{}}
	if len(c.Quality) < 2 {
		return result
	}
	latest := c.Quality[len(c.Quality)-1]
	if effectiveQualityDecision(latest) != "FAIL" {
		return result
	}
	var previous *QualityDecision
	for i := len(c.Quality) - 2; i >= 0; i-- {
		if c.Quality[i].Generation == latest.Generation-1 && effectiveQualityDecision(c.Quality[i]) == "FAIL" {
			previous = &c.Quality[i]
			break
		}
	}
	if previous == nil {
		return result
	}
	old := map[string]bool{}
	for _, category := range effectiveFailureCategories(*previous) {
		old[category] = true
	}
	for _, category := range effectiveFailureCategories(latest) {
		if old[category] {
			result.TriggeredCategories = append(result.TriggeredCategories, category)
		}
	}
	if len(result.TriggeredCategories) > 0 {
		sort.Strings(result.TriggeredCategories)
		result.Required = true
		result.FailureGenerations = []int{previous.Generation, latest.Generation}
	}
	return result
}

func effectiveQualityDecision(q QualityDecision) string {
	if q.Adjudication != nil {
		return q.Adjudication.Decision
	}
	return q.Decision
}

func effectiveFailureCategories(q QualityDecision) []string {
	if q.Adjudication != nil && q.Adjudication.Decision == "FAIL" {
		return q.Adjudication.FailureCategories
	}
	return q.FailureCategories
}

func (c *DigitizationCase) validateEscalation(r *RecaptureAction, failed QualityDecision) error {
	required := c.EscalationRequirement()
	if !required.Required {
		return nil
	}
	if r.Escalation == nil {
		return Conflict("连续质量失败需要升级授权", map[string]interface{}{"escalation_required": true, "triggered_categories": required.TriggeredCategories, "failure_generations": required.FailureGenerations})
	}
	e := r.Escalation
	e.PreservationOfficer, e.RiskDisposition = strings.TrimSpace(e.PreservationOfficer), strings.TrimSpace(e.RiskDisposition)
	if e.PreservationOfficer == "" || e.RiskDisposition == "" || e.MaximumAdditionalAttempts < 1 {
		return Invalid("升级授权材料不完整", map[string]interface{}{"triggered_categories": required.TriggeredCategories})
	}
	prohibited := []string{c.Plan.Operator, failed.Reviewer, r.AuthorizedBy}
	if failed.Adjudication != nil {
		prohibited = append(prohibited, failed.Adjudication.Adjudicator)
	}
	for _, countersign := range failed.Countersigns {
		prohibited = append(prohibited, countersign.Reviewer)
	}
	for _, prior := range c.Recaptures {
		prohibited = append(prohibited, prior.AuthorizedBy)
	}
	separated := true
	for _, person := range prohibited {
		if strings.EqualFold(e.PreservationOfficer, person) {
			separated = false
			break
		}
	}
	if !separated {
		return Conflict("保存负责人不满足人员分离要求", map[string]interface{}{"preservation_officer": e.PreservationOfficer})
	}
	covered := map[string]bool{}
	for _, category := range e.TriggeredCategories {
		covered[strings.ToLower(strings.TrimSpace(category))] = true
	}
	missing := []string{}
	for _, category := range required.TriggeredCategories {
		if !covered[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 {
		return Invalid("升级授权未覆盖持续失败类别", map[string]interface{}{"missing_categories": missing})
	}
	e.Required, e.TriggeredCategories, e.FailureGenerations, e.RemainingAttempts = true, required.TriggeredCategories, required.FailureGenerations, e.MaximumAdditionalAttempts
	return nil
}

func (c *DigitizationCase) BuildComponentProof(component string, generation int) (ComponentProof, error) {
	if c.State != StateSealed || c.Manifest == nil {
		return ComponentProof{}, Conflict("保存包清单不可用", nil)
	}
	component = strings.ToLower(strings.TrimSpace(component))
	if generation != 0 && component != "captures" {
		return ComponentProof{}, Invalid("generation 只能与 captures 组件组合", map[string]interface{}{"component": component})
	}
	payload := c.Manifest.CanonicalPayload
	digests := c.Manifest.ComponentDigests
	var content interface{}
	var expected string
	switch component {
	case "registration":
		content = registrationComponent(payload)
		expected = digests.Registration
	case "assessment":
		content = assessmentComponent(payload)
		expected = digests.Assessment
	case "plans":
		content = plansComponent(payload)
		expected = digests.Plans
	case "captures":
		captures := payload.Captures
		if generation > 0 {
			filtered := []CaptureGeneration{}
			for _, capture := range captures {
				if capture.Generation == generation {
					filtered = append(filtered, capture)
				}
			}
			if len(filtered) == 0 {
				return ComponentProof{}, Invalid("采集代次不存在", map[string]interface{}{"generation": generation})
			}
			captures = filtered
			expected = stableDigest(captures)
		} else {
			expected = digests.Captures
		}
		content = captures
	case "quality":
		content = payload.Quality
		expected = digests.Quality
	case "recaptures":
		content = payload.Recaptures
		expected = digests.Recaptures
	default:
		return ComponentProof{}, Invalid("未知保存包组件", map[string]interface{}{"component": component})
	}
	actual := stableDigest(content)
	formal := componentDigests(payload)
	formalMap := map[string]string{"registration": formal.Registration, "assessment": formal.Assessment, "plans": formal.Plans, "captures": formal.Captures, "quality": formal.Quality, "recaptures": formal.Recaptures}
	current := componentDigests(c.manifestPayload())
	currentMap := map[string]string{"registration": current.Registration, "assessment": current.Assessment, "plans": current.Plans, "captures": current.Captures, "quality": current.Quality, "recaptures": current.Recaptures}
	contentOK := actual == expected
	contentOK = contentOK && formalMap[component] == expected
	if generation > 0 {
		currentGeneration := []CaptureGeneration{}
		for _, capture := range c.Captures {
			if capture.Generation == generation {
				currentGeneration = append(currentGeneration, capture)
			}
		}
		contentOK = actual == expected && stableDigest(currentGeneration) == expected
	} else {
		contentOK = contentOK && currentMap[component] == expected
	}
	proof := []string{"registration:" + digests.Registration, "assessment:" + digests.Assessment, "plans:" + digests.Plans, "captures:" + digests.Captures, "quality:" + digests.Quality, "recaptures:" + digests.Recaptures}
	verification := c.ManifestVerification()
	mismatch := ""
	if !contentOK {
		mismatch = "component_content"
	} else if !verification.Valid {
		mismatch = "proof_path"
	}
	return ComponentProof{Component: component, Generation: generation, Content: content, ComponentDigest: expected, ProofPath: proof, CanonicalPayloadDigest: c.Manifest.CanonicalPayloadDigest, Verification: map[string]bool{"component_content": contentOK, "proof_path": verification.Valid, "audit_anchor": c.Manifest.AuditHeadDigest != "", "canonical_payload": payloadDigest(payload) == c.Manifest.CanonicalPayloadDigest}, MismatchLevel: mismatch}, nil
}
