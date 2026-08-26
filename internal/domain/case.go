package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

func formatFailureDetail(v interface{}) string { return fmt.Sprint(v) }

var accessionRE = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,63}$`)

func NormalizeAccession(v string) (string, error) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if !accessionRE.MatchString(v) {
		return "", ErrInvalid
	}
	return v, nil
}

func NewCase(id, accession, title, rights, carrier, scope string) (*DigitizationCase, error) {
	return NewCaseWithReceipt(id, accession, title, rights, carrier, scope, nil)
}

func NewCaseWithReceipt(id, accession, title, rights, carrier, scope string, intake *IntakeReceipt) (*DigitizationCase, error) {
	return NewCaseWithEvidence(id, accession, title, rights, carrier, scope, intake, nil)
}

func NewCaseWithEvidence(id, accession, title, rights, carrier, scope string, intake *IntakeReceipt, facets []CarrierFacet) (*DigitizationCase, error) {
	accession, e := NormalizeAccession(accession)
	if e != nil {
		return nil, e
	}
	id, title, rights, carrier, scope = strings.TrimSpace(id), strings.TrimSpace(title), strings.TrimSpace(rights), strings.TrimSpace(carrier), strings.TrimSpace(scope)
	if id == "" || title == "" || rights == "" || carrier == "" || scope == "" {
		return nil, ErrInvalid
	}
	now := time.Now().UTC()
	var receipt *IntakeReceipt
	if intake != nil {
		normalized, err := NormalizeIntakeReceipt(*intake, now)
		if err != nil {
			return nil, err
		}
		receipt = &normalized
	}
	if facets == nil {
		facets = []CarrierFacet{{FacetID: "MAIN", Label: "主载体", PhysicalOrder: 1, ContentScope: scope, Playable: true}}
	}
	normalizedFacets, facetDigest, err := NormalizeCarrierFacets(facets)
	if err != nil {
		return nil, err
	}
	return &DigitizationCase{ID: id, AccessionCode: accession, Title: title, RightsNote: rights, CarrierType: carrier, ContentScope: scope, CarrierFacets: normalizedFacets, CarrierFacetsDigest: facetDigest, AlternativeIdentifiers: []AlternativeIdentifier{}, AssessmentHistory: []ConditionAssessment{}, IntakeReceipt: receipt, State: StateRegistered, Revision: 1, CreatedAt: now, FirstAuditAt: now, PlanHistory: []CapturePlan{}, Captures: []CaptureGeneration{}, Quality: []QualityDecision{}, Recaptures: []RecaptureAction{}}, nil
}
func (c *DigitizationCase) mutable() error {
	if c.State == StateSealed {
		return ErrSealed
	}
	return nil
}
func (c *DigitizationCase) Assess(a ConditionAssessment) error {
	if e := c.mutable(); e != nil {
		return e
	}
	if c.State != StateRegistered {
		return ErrState
	}
	a.Assessor = strings.TrimSpace(a.Assessor)
	a.RequiredTreatment = strings.TrimSpace(a.RequiredTreatment)
	a.ContaminationNotes = strings.TrimSpace(a.ContaminationNotes)
	a.MoldLevel = strings.ToLower(strings.TrimSpace(a.MoldLevel))
	a.PlaybackRisk = strings.ToLower(strings.TrimSpace(a.PlaybackRisk))
	if a.Assessor == "" {
		return ErrInvalid
	}
	if (strings.EqualFold(a.MoldLevel, "high") || a.Contamination) && strings.TrimSpace(a.ContaminationNotes) == "" {
		return ErrInvalid
	}
	if !validLevel(a.MoldLevel) || !validLevel(a.PlaybackRisk) {
		return ErrInvalid
	}
	parts := []string{}
	required := []string{}
	if a.MoldLevel != "none" {
		parts = append(parts, "mold:"+strings.ToLower(a.MoldLevel))
		required = append(required, "mold")
	}
	if a.Breakage {
		parts = append(parts, "breakage")
		required = append(required, "breakage")
	}
	if a.Adhesion {
		parts = append(parts, "adhesion")
		required = append(required, "adhesion")
	}
	if a.Contamination {
		parts = append(parts, "contamination")
		required = append(required, "contamination")
	}
	if a.PlaybackRisk != "none" {
		parts = append(parts, "playback:"+strings.ToLower(a.PlaybackRisk))
		if a.PlaybackRisk == "high" {
			required = append(required, "playback")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "low-risk")
	}
	submittedAt := time.Now().UTC()
	coverage, evidence, err := validateTreatmentEvidence(required, a.TreatmentEvidence, a.NoTreatmentRequired, submittedAt)
	if err != nil {
		return err
	}
	a.RiskSummary = strings.Join(parts, ",")
	a.RiskCategories = append([]string(nil), parts...)
	a.TreatmentCoverage = coverage
	a.TreatmentEvidence = evidence
	acclimatization, acclimatizationErr := normalizeAcclimatization(a.Acclimatization, c.CarrierType, a, submittedAt)
	if acclimatizationErr != nil {
		return acclimatizationErr
	}
	a.Acclimatization = acclimatization
	// 观察证据字段为新版本可选扩展；一旦提交即执行完整覆盖校验。
	if a.ObservationEvidence != nil {
		observations, observationDigest, observationErr := normalizeObservationEvidence(a.ObservationEvidence, identifiedRiskCategories(a), submittedAt)
		if observationErr != nil {
			return observationErr
		}
		a.ObservationEvidence, a.ObservationEvidenceDigest = observations, observationDigest
	}
	locations, damageSummaries, damageDigest, damageErr := normalizeDamageLocations(c.CarrierFacets, a)
	if damageErr != nil {
		return damageErr
	}
	a.DamageLocations, a.DamageSummaries, a.DamageLocationDigest = locations, damageSummaries, damageDigest
	a.CaseID = c.ID
	a.AssessedAt = submittedAt
	a.AssessmentVersion = 1
	c.Assessment = &a
	c.State = StateAssessed
	c.Revision++
	return nil
}

func validateTreatmentEvidence(required []string, evidence []TreatmentEvidence, noTreatment bool, now time.Time) (map[string]string, []TreatmentEvidence, error) {
	if len(required) == 0 {
		if !noTreatment || len(evidence) != 0 {
			return nil, nil, Invalid("无必需处置类别时必须明确声明无需处置", nil)
		}
		return map[string]string{}, []TreatmentEvidence{}, nil
	}
	if noTreatment {
		return nil, nil, Invalid("已识别风险必须提交处置证据", map[string]interface{}{"required_categories": required})
	}
	requiredSet := map[string]bool{}
	for _, category := range required {
		requiredSet[category] = true
	}
	seen := map[string]bool{}
	coverage := map[string]string{}
	normalized := make([]TreatmentEvidence, 0, len(evidence))
	for _, item := range evidence {
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		item.Action = strings.TrimSpace(item.Action)
		item.PerformedBy = strings.TrimSpace(item.PerformedBy)
		item.EvidenceSummary = strings.TrimSpace(item.EvidenceSummary)
		if !requiredSet[item.Category] {
			return nil, nil, Invalid("存在非必需的处置类别", map[string]interface{}{"category": item.Category})
		}
		if seen[item.Category] {
			return nil, nil, Invalid("处置类别重复", map[string]interface{}{"category": item.Category})
		}
		if item.Action == "" || item.PerformedBy == "" || item.CompletedAt.IsZero() || item.CompletedAt.After(now) || !validSummary(item.EvidenceSummary) {
			return nil, nil, Invalid("处置证据字段无效", map[string]interface{}{"category": item.Category})
		}
		item.CompletedAt = item.CompletedAt.UTC()
		seen[item.Category] = true
		coverage[item.Category] = item.EvidenceSummary
		normalized = append(normalized, item)
	}
	missing := []string{}
	for _, category := range required {
		if !seen[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 {
		return nil, nil, Invalid("处置证据未覆盖全部风险", map[string]interface{}{"missing_categories": missing, "required_categories": required})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Category < normalized[j].Category })
	return coverage, normalized, nil
}

func validSummary(v string) bool {
	if len([]rune(v)) < 4 || len([]rune(v)) > 500 {
		return false
	}
	for _, r := range v {
		if r < 0x20 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func validLevel(v string) bool {
	switch strings.ToLower(v) {
	case "none", "low", "medium", "high":
		return true
	}
	return false
}
func (c *DigitizationCase) ApprovePlan(p CapturePlan) error {
	if e := c.mutable(); e != nil {
		return e
	}
	isRevision := c.State == StateReady
	if c.State != StateAssessed && !isRevision {
		return ErrState
	}
	if isRevision && (len(c.Captures) > 0 || len(c.Quality) > 0 || c.Manifest != nil) {
		return Conflict("当前采集方案已不能修订", map[string]interface{}{"current_plan_revision": c.Plan.PlanRevision, "current_plan_fingerprint": c.Plan.Fingerprint})
	}
	p.RevisionReason = strings.TrimSpace(p.RevisionReason)
	if isRevision && p.RevisionReason == "" {
		return Invalid("修订方案必须填写 revision_reason", nil)
	}
	p.PlaybackDevice, p.SignalChain, p.TargetCodec, p.ChannelMap, p.Operator, p.ApprovedBy = strings.TrimSpace(p.PlaybackDevice), strings.TrimSpace(p.SignalChain), strings.ToLower(strings.TrimSpace(p.TargetCodec)), strings.TrimSpace(p.ChannelMap), strings.TrimSpace(p.Operator), strings.TrimSpace(p.ApprovedBy)
	if p.PlaybackDevice == "" || p.SignalChain == "" || p.TargetCodec == "" || p.ChannelMap == "" || p.Operator == "" || p.ApprovedBy == "" {
		return ErrInvalid
	}
	if strings.EqualFold(p.Operator, p.ApprovedBy) {
		return ErrInvalid
	}
	mins := map[string][2]int{"wav": {44100, 16}, "bwf": {44100, 16}, "flac": {44100, 16}, "aiff": {44100, 16}}
	min, ok := mins[p.TargetCodec]
	if !ok || p.SampleRateHz < min[0] || p.BitDepth < min[1] {
		return ErrInvalid
	}
	if p.SampleRateHz <= 0 || p.BitDepth <= 0 || !validChannelMap(p.ChannelMap) {
		return ErrInvalid
	}
	validRates := map[int]bool{44100: true, 48000: true, 88200: true, 96000: true, 176400: true, 192000: true}
	validBits := map[int]bool{16: true, 24: true, 32: true}
	if !validRates[p.SampleRateHz] || !validBits[p.BitDepth] {
		return ErrInvalid
	}
	if p.RiskControls != nil || p.NoAdditionalControls {
		controls, covered, controlDigest, controlErr := normalizeRiskControls(p.RiskControls, p.NoAdditionalControls, identifiedRiskCategories(*c.Assessment))
		if controlErr != nil {
			return controlErr
		}
		p.RiskControls, p.CoveredRiskCategories, p.RiskControlDigest = controls, covered, controlDigest
	}
	tasks, skipped, coverageDigest, totalDuration, taskErr := normalizeCaptureTasks(c.CarrierFacets, p.CaptureTasks, p.SkippedFacets, p.ChannelMap)
	if taskErr != nil {
		return taskErr
	}
	p.CaptureTasks, p.SkippedFacets, p.TaskCoverageDigest, p.EstimatedTotalDurationMs = tasks, skipped, coverageDigest, totalDuration
	channels := channelCount(p.ChannelMap)
	carrier := strings.ToLower(c.CarrierType)
	if (strings.Contains(carrier, "stereo") || strings.Contains(carrier, "双声道")) && channels != 2 {
		return ErrInvalid
	}
	if (strings.Contains(carrier, "mono") || strings.Contains(carrier, "单声道")) && channels != 1 {
		return ErrInvalid
	}
	p.CaseID = c.ID
	p.ApprovedAt = time.Now().UTC().Truncate(time.Second)
	if p.ValidUntil.IsZero() {
		// 为既有客户端保留确定的默认窗口；新客户端应显式提交 valid_until。
		p.ValidUntil = p.ApprovedAt.Add(30 * 24 * time.Hour)
	}
	p.ValidUntil = p.ValidUntil.UTC()
	if !p.ValidUntil.After(p.ApprovedAt) || p.ValidUntil.Sub(p.ApprovedAt) > 90*24*time.Hour {
		return Invalid("方案有效期无效", map[string]interface{}{"field": "valid_until", "maximum_days": 90})
	}
	p.ValidityStatus = "ACTIVE"
	if err := validatePlanWindow(&p); err != nil {
		return err
	}
	if isRevision {
		if p.ReapprovalReason == "" {
			p.ReapprovalReason = p.RevisionReason
		}
		p.ReapprovalReason = strings.TrimSpace(p.ReapprovalReason)
		if p.ReapprovalReason == "" {
			return Invalid("复批必须填写 reapproval_reason", map[string]interface{}{"field": "reapproval_reason"})
		}
		p.ReapprovesPlanRevision = c.Plan.PlanRevision
		p.PlanRevision = c.Plan.PlanRevision + 1
		p.ChangedFields = changedPlanFields(*c.Plan, p)
		if len(p.ChangedFields) == 0 {
			return Invalid("修订方案必须至少改变一个受控字段", nil)
		}
	} else {
		p.PlanRevision = c.Revision + 1
		p.ChangedFields = []string{}
	}
	p.Fingerprint = planFingerprint(p)
	if isRevision && c.Plan != nil && !c.Plan.ScheduledStart.IsZero() {
		c.Plan.ReservationStatus = "RELEASED"
		for i := range c.PlanHistory {
			if c.PlanHistory[i].PlanRevision == c.Plan.PlanRevision {
				c.PlanHistory[i].ReservationStatus = "RELEASED"
			}
		}
	}
	c.Plan = &p
	c.PlanHistory = append(c.PlanHistory, p)
	c.State = StateReady
	c.Revision++
	return nil
}

// ReleasePlanReservation 主动释放尚未开始采集的预约，但保留个案在待采集状态。
func (c *DigitizationCase) ReleasePlanReservation(releasedBy, reason string) error {
	if e := c.mutable(); e != nil {
		return e
	}
	if c.State != StateReady || c.Plan == nil {
		return Conflict("当前状态不允许释放预约", map[string]interface{}{"state": c.State})
	}
	if len(c.Captures) > 0 || c.CurrentCaptureGeneration > 0 {
		return Conflict("已有采集证据，不能释放预约", nil)
	}
	if c.Plan.ScheduledStart.IsZero() || c.Plan.ScheduledEnd.IsZero() {
		return Conflict("当前方案没有有效预约", nil)
	}
	if strings.ToUpper(strings.TrimSpace(c.Plan.ReservationStatus)) != "ACTIVE" {
		return Conflict("预约当前不可释放", map[string]interface{}{"reservation_status": c.Plan.ReservationStatus})
	}
	if time.Now().UTC().After(c.Plan.ValidUntil) || time.Now().UTC().After(c.Plan.ScheduledEnd) {
		return Conflict("预约已失效，不能释放", map[string]interface{}{"reservation_status": "EXPIRED"})
	}
	releasedBy = strings.Join(strings.Fields(releasedBy), " ")
	reason = strings.TrimSpace(reason)
	if releasedBy == "" || len([]rune(reason)) < 1 || len([]rune(reason)) > 500 {
		return Invalid("释放人或释放原因无效", map[string]interface{}{"field": "release_reason"})
	}
	now := time.Now().UTC()
	c.Plan.ReservationStatus = "RELEASED"
	c.Plan.ReservationReleasedAt = &now
	c.Plan.ReservationReleasedBy = releasedBy
	c.Plan.ReservationReleaseReason = reason
	for i := range c.PlanHistory {
		if c.PlanHistory[i].PlanRevision == c.Plan.PlanRevision {
			c.PlanHistory[i].ReservationStatus = "RELEASED"
			c.PlanHistory[i].ReservationReleasedAt = &now
			c.PlanHistory[i].ReservationReleasedBy = releasedBy
			c.PlanHistory[i].ReservationReleaseReason = reason
		}
	}
	c.Revision++
	return nil
}

func changedPlanFields(old, next CapturePlan) []string {
	changed := []string{}
	if old.PlaybackDevice != next.PlaybackDevice {
		changed = append(changed, "playback_device")
	}
	if old.SignalChain != next.SignalChain {
		changed = append(changed, "signal_chain")
	}
	if old.TargetCodec != next.TargetCodec {
		changed = append(changed, "target_codec")
	}
	if old.SampleRateHz != next.SampleRateHz {
		changed = append(changed, "sample_rate_hz")
	}
	if old.BitDepth != next.BitDepth {
		changed = append(changed, "bit_depth")
	}
	if old.ChannelMap != next.ChannelMap {
		changed = append(changed, "channel_map")
	}
	if old.Operator != next.Operator {
		changed = append(changed, "operator")
	}
	if old.ApprovedBy != next.ApprovedBy {
		changed = append(changed, "approved_by")
	}
	if old.RiskControlDigest != next.RiskControlDigest || old.NoAdditionalControls != next.NoAdditionalControls {
		changed = append(changed, "risk_controls")
	}
	if old.TaskCoverageDigest != next.TaskCoverageDigest {
		changed = append(changed, "capture_tasks")
	}
	if !old.ValidUntil.Equal(next.ValidUntil) {
		changed = append(changed, "valid_until")
	}
	if !old.ScheduledStart.Equal(next.ScheduledStart) || !old.ScheduledEnd.Equal(next.ScheduledEnd) {
		changed = append(changed, "scheduled_window")
	}
	return changed
}

type planPayload struct {
	PlaybackDevice       string         `json:"playback_device"`
	SignalChain          string         `json:"signal_chain"`
	TargetCodec          string         `json:"target_codec"`
	SampleRateHz         int            `json:"sample_rate_hz"`
	BitDepth             int            `json:"bit_depth"`
	ChannelMap           string         `json:"channel_map"`
	Operator             string         `json:"operator"`
	ApprovedBy           string         `json:"approved_by"`
	RiskControls         []RiskControl  `json:"risk_controls"`
	NoAdditionalControls bool           `json:"no_additional_controls"`
	RiskControlDigest    string         `json:"risk_control_digest"`
	CaptureTasks         []CaptureTask  `json:"capture_tasks"`
	SkippedFacets        []SkippedFacet `json:"skipped_facets"`
	TaskCoverageDigest   string         `json:"task_coverage_digest"`
	ValidUntil           time.Time      `json:"valid_until"`
	ScheduledStart       time.Time      `json:"scheduled_start,omitempty"`
	ScheduledEnd         time.Time      `json:"scheduled_end,omitempty"`
}

func planFingerprint(p CapturePlan) string {
	b, _ := json.Marshal(planPayload{p.PlaybackDevice, p.SignalChain, p.TargetCodec, p.SampleRateHz, p.BitDepth, p.ChannelMap, p.Operator, p.ApprovedBy, p.RiskControls, p.NoAdditionalControls, p.RiskControlDigest, p.CaptureTasks, p.SkippedFacets, p.TaskCoverageDigest, p.ValidUntil, p.ScheduledStart, p.ScheduledEnd})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validChannelMap(v string) bool {
	seen := map[string]bool{}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == ',' || r == ';' || r == '|' })
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			return false
		}
		seen[strings.ToLower(p)] = true
	}
	return true
}
func channelCount(v string) int {
	return len(strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == ',' || r == ';' || r == '|' }))
}
func (c *DigitizationCase) AddCapture(g CaptureGeneration) error {
	if e := c.mutable(); e != nil {
		return e
	}
	if c.State != StateReady {
		return ErrState
	}
	if c.Plan == nil || g.PlanRevision != c.Plan.PlanRevision {
		details := map[string]interface{}{}
		if c.Plan != nil {
			details["current_plan_revision"] = c.Plan.PlanRevision
			details["current_plan_fingerprint"] = c.Plan.Fingerprint
		}
		return Conflict("采集引用的不是当前方案", details)
	}
	if strings.EqualFold(c.Plan.ReservationStatus, "RELEASED") {
		return Conflict("当前方案预约已释放，必须重新排期", map[string]interface{}{"reservation_status": "RELEASED", "plan_revision": c.Plan.PlanRevision})
	}
	if g.StartedAt.Before(c.Plan.ApprovedAt) || g.StartedAt.After(c.Plan.ValidUntil) {
		return Conflict("当前采集方案已过期或尚未生效", map[string]interface{}{"valid_until": c.Plan.ValidUntil, "approved_at": c.Plan.ApprovedAt, "current_plan_revision": c.Plan.PlanRevision, "validity_status": "EXPIRED"})
	}
	if !c.Plan.ScheduledStart.IsZero() && (g.StartedAt.Before(c.Plan.ScheduledStart) || g.EndedAt.After(c.Plan.ScheduledEnd)) {
		return Conflict("采集证据不在批准的资源时窗内", map[string]interface{}{"scheduled_start": c.Plan.ScheduledStart, "scheduled_end": c.Plan.ScheduledEnd})
	}
	g.CaptureTaskID = strings.ToUpper(strings.TrimSpace(g.CaptureTaskID))
	if g.CaptureTaskID == "" && len(c.Plan.CaptureTasks) == 1 {
		g.CaptureTaskID = c.Plan.CaptureTasks[0].TaskID
	}
	taskFound := false
	for _, task := range c.Plan.CaptureTasks {
		if task.TaskID == g.CaptureTaskID {
			taskFound = true
		}
	}
	if !taskFound {
		return Conflict("采集任务不属于当前方案", map[string]interface{}{"capture_task_id": g.CaptureTaskID, "plan_revision": c.Plan.PlanRevision})
	}
	if supplied := strings.TrimSpace(g.PlanFingerprint); supplied != "" && !strings.EqualFold(supplied, c.Plan.Fingerprint) {
		return Conflict("采集引用的不是当前方案", map[string]interface{}{"current_plan_revision": c.Plan.PlanRevision, "current_plan_fingerprint": c.Plan.Fingerprint})
	}
	g.CalibrationReference = strings.ToUpper(strings.TrimSpace(g.CalibrationReference))
	g.CalibrationDevice = strings.TrimSpace(g.CalibrationDevice)
	g.AssetDigest = strings.ToLower(strings.TrimSpace(g.AssetDigest))
	g.ContainerFormat = strings.ToLower(strings.TrimSpace(g.ContainerFormat))
	g.ActualCodec = strings.ToLower(strings.TrimSpace(g.ActualCodec))
	g.PlanFingerprint = c.Plan.Fingerprint
	windowMs := int64(0)
	if !g.StartedAt.IsZero() && !g.EndedAt.IsZero() && g.EndedAt.After(g.StartedAt) {
		windowMs = int64(g.EndedAt.Sub(g.StartedAt) / time.Millisecond)
	}
	if g.CalibrationReference == "" || g.CalibrationDevice == "" || g.StartedAt.IsZero() || g.EndedAt.IsZero() || !g.EndedAt.After(g.StartedAt) || windowMs > int64(24*time.Hour/time.Millisecond) || g.DurationMs <= 0 || g.DurationMs > windowMs || g.DurationMs > int64(24*time.Hour/time.Millisecond) || !validDigest(g.AssetDigest) || g.PeakDBFS > 0 || g.PeakDBFS < -120 {
		return ErrInvalid
	}
	if !strings.EqualFold(g.CalibrationDevice, c.Plan.PlaybackDevice) {
		return Invalid("校准设备与批准的播放设备不一致", map[string]interface{}{"calibration_device": g.CalibrationDevice, "playback_device": c.Plan.PlaybackDevice})
	}
	if g.CalibratedAt.IsZero() || g.CalibrationValidUntil.IsZero() || !g.CalibrationValidUntil.After(g.CalibratedAt) || g.CalibrationValidUntil.Sub(g.CalibratedAt) > 366*24*time.Hour {
		return Invalid("校准有效期无效", nil)
	}
	if g.StartedAt.Before(g.CalibratedAt) || g.EndedAt.After(g.CalibrationValidUntil) {
		return Invalid("采集时段不在校准有效期内", map[string]interface{}{"calibrated_at": g.CalibratedAt.UTC(), "calibration_valid_until": g.CalibrationValidUntil.UTC()})
	}
	knownContainers := map[string]bool{"wav": true, "bwf": true, "aiff": true, "flac": true}
	if g.AssetSizeBytes <= 0 || g.AssetSizeBytes > 16*1024*1024*1024*1024 || !knownContainers[g.ContainerFormat] || g.ActualSampleRateHz <= 0 || g.ActualBitDepth <= 0 || g.ActualChannels <= 0 || g.ActualChannels > 32 {
		return Invalid("源文件技术参数无效", nil)
	}
	mismatches := []string{}
	if g.ActualCodec != c.Plan.TargetCodec {
		mismatches = append(mismatches, "actual_codec")
	}
	if g.ActualSampleRateHz != c.Plan.SampleRateHz {
		mismatches = append(mismatches, "actual_sample_rate_hz")
	}
	if g.ActualBitDepth != c.Plan.BitDepth {
		mismatches = append(mismatches, "actual_bit_depth")
	}
	if g.ActualChannels != channelCount(c.Plan.ChannelMap) {
		mismatches = append(mismatches, "actual_channels")
	}
	if len(mismatches) > 0 {
		return Invalid("源文件参数与当前方案不一致", map[string]interface{}{"mismatch_fields": mismatches})
	}
	if g.ContainerFormat != "flac" {
		expected := g.DurationMs * int64(g.ActualSampleRateHz) * int64(g.ActualBitDepth) * int64(g.ActualChannels) / 8000
		minimum, maximum := expected*80/100, expected*125/100+1024*1024
		if g.AssetSizeBytes < minimum || g.AssetSizeBytes > maximum {
			return Invalid("未压缩源文件大小超出合理区间", map[string]interface{}{"minimum_size_bytes": minimum, "maximum_size_bytes": maximum})
		}
	}
	if err := normalizeFileSegments(&g); err != nil {
		return err
	}
	if err := normalizeFixity(&g); err != nil {
		return err
	}
	if err := normalizeCalibrationEvidence(&g, channelNames(c.Plan.ChannelMap)); err != nil {
		return err
	}
	if g.OperationEvents != nil {
		events, pausedMs, calculatedMs, timelineDigest, timelineErr := normalizeOperationEvents(g.OperationEvents, g.StartedAt.UTC(), g.EndedAt.UTC(), g.DurationMs)
		if timelineErr != nil {
			return timelineErr
		}
		g.OperationEvents, g.PausedDurationMs, g.CalculatedAudioDurationMs, g.OperationTimelineDigest = events, pausedMs, calculatedMs, timelineDigest
	}
	for _, old := range c.Captures {
		if strings.EqualFold(old.AssetDigest, g.AssetDigest) {
			return ErrConflict
		}
	}
	next := c.CurrentCaptureGeneration + 1
	if g.Generation != 0 && g.Generation != next {
		return ErrConflict
	}
	g.Generation = next
	g.CaseID = c.ID
	if next > 1 {
		if len(c.Recaptures) == 0 {
			return Conflict("缺少重采授权", nil)
		}
		r, authorizationIndex := c.activeAuthorization(next)
		if r == nil || r.ConsumedAt != nil || r.Status != "ACTIVE" {
			return Conflict("重采授权不是当前有效版本", map[string]interface{}{"generation": next})
		}
		if g.RecaptureAuthorizationVersion != 0 && g.RecaptureAuthorizationVersion != r.AuthorizationVersion {
			return Conflict("采集引用的重采授权版本已失效", map[string]interface{}{"current_authorization_version": r.AuthorizationVersion})
		}
		if g.StartedAt.Before(r.AuthorizedAt) || g.StartedAt.After(r.ExpiresAt) {
			return Conflict("采集开始时重采授权不在有效期内", map[string]interface{}{"authorized_at": r.AuthorizedAt, "expires_at": r.ExpiresAt})
		}
		g.RecaptureReason = r.Reason
		g.AuthorizedBy = r.AuthorizedBy
		consumed := time.Now().UTC()
		r.ConsumedAt = &consumed
		r.ConsumedGeneration = next
		r.Status = "CONSUMED"
		c.Recaptures[authorizationIndex] = *r
		g.RecaptureAuthorizationVersion = r.AuthorizationVersion
		if supplied := strings.TrimSpace(g.RecaptureRemediationDigest); supplied != "" && !strings.EqualFold(supplied, r.RemediationEvidenceDigest) {
			return Conflict("采集引用的整改证据摘要已变化", map[string]interface{}{"current_remediation_evidence_digest": r.RemediationEvidenceDigest})
		}
		g.RecaptureRemediationDigest = r.RemediationEvidenceDigest
		if r.Escalation != nil {
			if r.Escalation.RemainingAttempts <= 0 {
				return Conflict("升级授权尝试额度已耗尽", map[string]interface{}{"authorization_version": r.AuthorizationVersion})
			}
			r.Escalation.RemainingAttempts--
			c.Recaptures[authorizationIndex] = *r
		}
	}
	g.CalibratedAt = g.CalibratedAt.UTC()
	g.CalibrationValidUntil = g.CalibrationValidUntil.UTC()
	g.StartedAt = g.StartedAt.UTC()
	g.EndedAt = g.EndedAt.UTC()
	g.TechnicalEvidenceDigest = captureTechnicalDigest(g)
	c.Captures = append(c.Captures, g)
	c.consumePlanReservation(g.EndedAt)
	c.CurrentCaptureGeneration = next
	c.State = StateCaptured
	c.Revision++
	return nil
}

func captureTechnicalDigest(g CaptureGeneration) string {
	payload := struct {
		CalibrationReference      string        `json:"calibration_reference"`
		CalibrationDevice         string        `json:"calibration_device"`
		CalibratedAt              time.Time     `json:"calibrated_at"`
		ValidUntil                time.Time     `json:"calibration_valid_until"`
		AssetDigest               string        `json:"asset_digest"`
		AssetSize                 int64         `json:"asset_size_bytes"`
		Container                 string        `json:"container_format"`
		Codec                     string        `json:"actual_codec"`
		Rate                      int           `json:"actual_sample_rate_hz"`
		Bits                      int           `json:"actual_bit_depth"`
		Channels                  int           `json:"actual_channels"`
		StartedAt                 time.Time     `json:"started_at"`
		EndedAt                   time.Time     `json:"ended_at"`
		DurationMs                int64         `json:"duration_ms"`
		PeakDBFS                  float64       `json:"peak_dbfs"`
		PlanRevision              int64         `json:"plan_revision"`
		PlanFingerprint           string        `json:"plan_fingerprint"`
		OperationTimelineDigest   string        `json:"operation_timeline_digest"`
		CaptureTaskID             string        `json:"capture_task_id"`
		FixityDigest              string        `json:"fixity_digest"`
		CalibrationEvidenceDigest string        `json:"calibration_evidence_digest"`
		FileSegments              []FileSegment `json:"file_segments,omitempty"`
		SegmentCombinationRule    string        `json:"segment_combination_rule,omitempty"`
		RemediationDigest         string        `json:"recapture_remediation_digest,omitempty"`
	}{g.CalibrationReference, g.CalibrationDevice, g.CalibratedAt.UTC(), g.CalibrationValidUntil.UTC(), g.AssetDigest, g.AssetSizeBytes, g.ContainerFormat, g.ActualCodec, g.ActualSampleRateHz, g.ActualBitDepth, g.ActualChannels, g.StartedAt.UTC(), g.EndedAt.UTC(), g.DurationMs, g.PeakDBFS, g.PlanRevision, g.PlanFingerprint, g.OperationTimelineDigest, g.CaptureTaskID, g.FixityDigest, g.CalibrationEvidenceDigest, g.FileSegments, g.SegmentCombinationRule, g.RecaptureRemediationDigest}
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CaptureTechnicalDigest exposes the canonical technical evidence calculation for read-only verification.
func CaptureTechnicalDigest(g CaptureGeneration) string { return captureTechnicalDigest(g) }
func validDigest(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, e := hex.DecodeString(v)
	return e == nil
}
func (c *DigitizationCase) DecideQuality(q QualityDecision) error {
	if e := c.mutable(); e != nil {
		return e
	}
	if c.State != StateCaptured {
		return ErrState
	}
	if q.AdjudicationForRevision != 0 {
		return c.applyAdjudication(q)
	}
	if q.CountersignForRevision != 0 {
		return c.applyCountersign(q)
	}
	q.Reviewer = strings.TrimSpace(q.Reviewer)
	q.ListeningNotes = strings.TrimSpace(q.ListeningNotes)
	if q.Generation != c.CurrentCaptureGeneration || q.Reviewer == "" || q.ListeningNotes == "" || q.ClippingEvents < 0 || q.DropoutEvents < 0 {
		return ErrInvalid
	}
	for _, old := range c.Quality {
		if old.Generation == q.Generation {
			return ErrConflict
		}
	}
	duration := int64(0)
	for _, capture := range c.Captures {
		if capture.Generation == q.Generation {
			duration = capture.DurationMs
		}
	}
	markers, impacts, summary, impactDigest, err := calculateDefectImpacts(q.DefectMarkers, duration, channelNames(c.Plan.ChannelMap))
	if err != nil {
		return err
	}
	clipping, dropout := 0, 0
	for _, marker := range markers {
		if marker.DefectType == "clipping" {
			clipping++
		}
		if marker.DefectType == "dropout" {
			dropout++
		}
	}
	if clipping != q.ClippingEvents || dropout != q.DropoutEvents {
		return Invalid("缺陷标记数量与质量汇总计数不一致", map[string]interface{}{"clipping_markers": clipping, "clipping_events": q.ClippingEvents, "dropout_markers": dropout, "dropout_events": q.DropoutEvents})
	}
	q.DefectMarkers, q.DefectImpacts, q.DefectSummary, q.DefectImpactDigest = markers, impacts, summary, impactDigest
	q.CaseID = c.ID
	q.ReviewedAt = time.Now().UTC()
	coverageFailureDetail := ""
	declaredDecision := strings.ToUpper(strings.TrimSpace(q.Decision))
	failures := qualityFailures(q)
	metrics, metricResults, metricFailures, thresholdVersion, metricErr := evaluateChannelMetrics(c.CarrierType, channelNames(c.Plan.ChannelMap), q.ChannelMetrics, q.MeasurementProfile)
	if metricErr != nil {
		return metricErr
	}
	q.ChannelMetrics, q.MetricResults, q.ThresholdVersion = metrics, metricResults, thresholdVersion
	failures = append(failures, metricFailures...)
	if q.ChannelMetrics != nil && declaredDecision == "" {
		return Invalid("提交量化指标时 decision 为必填项", map[string]interface{}{"calculated_decision": map[bool]string{true: "PASS", false: "FAIL"}[len(failures) == 0]})
	}
	if q.ListeningIntervals != nil {
		capture := c.Captures[len(c.Captures)-1]
		fullCoverage := q.Generation > 1 || (c.Assessment != nil && strings.EqualFold(c.Assessment.PlaybackRisk, "high"))
		intervals, coverage, coverageDigest, coverageErr := normalizeListeningIntervals(q.ListeningIntervals, duration, channelNames(c.Plan.ChannelMap), fullCoverage, markers)
		if coverageErr != nil {
			// 区间本身合法但覆盖不足属于质量失败，而不是请求格式错误；
			// 保留覆盖证据并继续生成失败决定。越界、倒序等结构错误仍拒绝请求。
			if detail, ok := coverageErr.(*DetailError); !ok || detail.Message != "人工听检覆盖不足" {
				return coverageErr
			} else {
				q.ListeningIntervals, q.ListeningCoverage, q.ListeningCoverageDigest = intervals, coverage, coverageDigest
				failures = append(failures, "listening_coverage")
				if missing, ok := detail.Details["missing_channels"]; ok {
					coverageFailureDetail = "missing_channels=" + formatFailureDetail(missing)
				}
			}
		} else {
			q.ListeningIntervals, q.ListeningCoverage, q.ListeningCoverageDigest = intervals, coverage, coverageDigest
		}
		_ = capture
	}
	if q.Generation > 1 && q.RemediationChecks != nil {
		authorization, _ := c.activeAuthorization(q.Generation)
		if authorization == nil {
			return Conflict("缺少本代重采授权", nil)
		}
		checks, resolved, persistent, newCategories, effectDigest, checkErr := normalizeRemediationChecks(q.RemediationChecks, *authorization, failures)
		if checkErr != nil {
			return checkErr
		}
		q.RemediationChecks, q.ResolvedCategories, q.PersistentCategories, q.NewCategories, q.RemediationEffectDigest = checks, resolved, persistent, newCategories, effectDigest
	}
	q.FailureCategories = failures
	q.FailureSummary = strings.Join(failures, ",")
	if coverageFailureDetail != "" {
		q.FailureSummary += ":" + coverageFailureDetail
	}
	if len(failures) == 0 {
		q.Decision = "PASS"
	} else {
		q.Decision = "FAIL"
	}
	if declaredDecision != "" && declaredDecision != q.Decision {
		return Invalid("调用方声明的质量决定与计算结果不一致", map[string]interface{}{"declared_decision": declaredDecision, "calculated_decision": q.Decision, "failure_categories": failures})
	}
	q.QualityRevision = c.Revision + 1
	q.QualityEvidenceDigest = stableDigest(struct {
		Generation   int            `json:"generation"`
		Decision     string         `json:"decision"`
		Failures     []string       `json:"failure_categories"`
		Defect       string         `json:"defect_summary"`
		Metrics      []MetricResult `json:"metric_results"`
		Threshold    string         `json:"threshold_version"`
		DefectImpact string         `json:"defect_impact_digest"`
		Listening    string         `json:"listening_coverage_digest"`
	}{q.Generation, q.Decision, q.FailureCategories, q.DefectSummary, q.MetricResults, q.ThresholdVersion, q.DefectImpactDigest, q.ListeningCoverageDigest})
	q.CountersignReasons = c.countersignReasons(q.Generation)
	q.RequiresCountersign = len(q.CountersignReasons) > 0
	if q.RequiresCountersign {
		q.CountersignStatus = "PENDING"
		c.State = StateCaptured
	} else if q.Decision == "PASS" {
		c.State = StateQCPassed
	} else {
		c.State = StateRecapture
	}
	c.Quality = append(c.Quality, q)
	c.Revision++
	return nil
}

func normalizeDefectMarkers(markers []DefectMarker, duration int64) ([]DefectMarker, string, error) {
	channels := []string{}
	for _, marker := range markers {
		channel := strings.ToUpper(strings.TrimSpace(marker.Channel))
		if channel != "" {
			channels = append(channels, channel)
		}
	}
	normalized, _, digest, _, err := calculateDefectImpacts(markers, duration, channels)
	return normalized, digest, err
}

func qualityFailures(q QualityDecision) []string {
	var failures []string
	if !q.CompletenessPassed {
		failures = append(failures, "completeness")
	}
	if q.ClippingEvents > 0 {
		failures = append(failures, "clipping")
	}
	if q.DropoutEvents > 0 {
		failures = append(failures, "dropout")
	}
	if !q.ChannelMappingPassed {
		failures = append(failures, "channel_mapping")
	}
	if q.DurationVarianceMs < -100 || q.DurationVarianceMs > 100 {
		failures = append(failures, "duration_variance")
	}
	for _, marker := range q.DefectMarkers {
		if marker.DefectType == "listening_anomaly" {
			failures = append(failures, "listening_anomaly")
			break
		}
	}
	return failures
}
func (c *DigitizationCase) AuthorizeRecapture(r RecaptureAction) error {
	action := strings.ToLower(strings.TrimSpace(r.Action))
	if action == "" || action == "authorize" {
		r.Action = "authorize"
		return c.authorizeRecaptureVersion(r, false)
	}
	if action == "revoke" {
		return c.RevokeRecapture(r)
	}
	if action == "renew" {
		return c.RenewRecapture(r)
	}
	return Invalid("未知重采授权 action", map[string]interface{}{"action": action})
}

func (c *DigitizationCase) authorizeRecaptureVersion(r RecaptureAction, renewal bool) error {
	if e := c.mutable(); e != nil {
		return e
	}
	if !renewal && c.State != StateRecapture {
		return ErrState
	}
	r.Reason, r.Remediation, r.AuthorizedBy = strings.TrimSpace(r.Reason), strings.TrimSpace(r.Remediation), strings.TrimSpace(r.AuthorizedBy)
	if r.AuthorizedBy == "" {
		return ErrInvalid
	}
	if len(c.Quality) == 0 || effectiveQualityDecision(c.Quality[len(c.Quality)-1]) != "FAIL" || c.Quality[len(c.Quality)-1].Generation != c.CurrentCaptureGeneration {
		return ErrConflict
	}
	failed := c.Quality[len(c.Quality)-1]
	failed.Decision = effectiveQualityDecision(failed)
	failed.FailureCategories = effectiveFailureCategories(failed)
	if err := c.validateEscalation(&r, failed); err != nil {
		return err
	}
	if r.RequestedFailedGeneration != 0 && r.RequestedFailedGeneration != failed.Generation {
		return Conflict("指定失败代次不是最近一次质量失败", map[string]interface{}{"failed_generation": failed.Generation})
	}
	if strings.EqualFold(r.AuthorizedBy, failed.Reviewer) {
		return Invalid("重采授权人不得与失败代次复核员相同", nil)
	}
	now := time.Now().UTC()
	if r.AuthorizedAt.IsZero() {
		r.AuthorizedAt = now
	}
	r.AuthorizedAt = r.AuthorizedAt.UTC()
	r.ExpiresAt = r.ExpiresAt.UTC()
	if r.AuthorizedAt.After(now.Add(5*time.Minute)) || !r.ExpiresAt.After(now) || !r.ExpiresAt.After(r.AuthorizedAt) || r.ExpiresAt.Sub(r.AuthorizedAt) > 30*24*time.Hour {
		return Invalid("重采授权有效期无效", nil)
	}
	required := failed.FailureCategories
	planOperator := ""
	if c.Plan != nil {
		planOperator = c.Plan.Operator
	}
	normalized, remediationDigest, remediationErr := normalizeCompletedRemediations(r.Remediations, required, failed, planOperator, r.AuthorizedBy, r.AuthorizedAt)
	if remediationErr != nil {
		return remediationErr
	}
	r.Remediations, r.RemediationEvidenceDigest = normalized, remediationDigest
	if r.Reason == "" {
		r.Reason = strings.Join(required, ",")
	}
	if r.Remediation == "" {
		actions := make([]string, len(normalized))
		for i := range normalized {
			actions[i] = normalized[i].Action
		}
		r.Remediation = strings.Join(actions, "; ")
	}
	if !renewal {
		r.Generation = c.CurrentCaptureGeneration + 1
		r.FailedQualityGeneration = c.CurrentCaptureGeneration
		r.AuthorizationVersion = 1
		r.Action = "authorize"
	}
	r.Status = "ACTIVE"
	r.At = r.AuthorizedAt
	c.Recaptures = append(c.Recaptures, r)
	c.State = StateReady
	c.Revision++
	return nil
}

func (c *DigitizationCase) manifestPayload() ManifestPayload {
	index, _ := c.BuildGenerationEvidenceIndex()
	return ManifestPayload{ID: c.ID, AccessionCode: c.AccessionCode, AlternativeIdentifiers: c.AlternativeIdentifiers, AlternativeIdentifierDigest: c.AlternativeIdentifierDigest, Title: c.Title, RightsNote: c.RightsNote, CarrierType: c.CarrierType, ContentScope: c.ContentScope, IntakeReceipt: c.IntakeReceipt, Assessment: c.Assessment, AssessmentHistory: c.AssessmentHistory, Plan: c.Plan, PlanHistory: c.PlanHistory, Captures: c.Captures, Quality: c.Quality, Recaptures: c.Recaptures, CarrierFacets: c.CarrierFacets, CarrierFacetsDigest: c.CarrierFacetsDigest, GenerationEvidenceIndex: index, CustodyEvents: c.CustodyEvents, CurrentCustodian: c.CurrentCustodian, CurrentLocationCode: c.CurrentLocationCode, CustodyChainDigest: c.CustodyChainDigest}
}
func payloadDigest(payload ManifestPayload) string {
	b, _ := json.Marshal(payload)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (c *DigitizationCase) BuildManifest(head, by string) (PreservationManifest, error) {
	if c.State != StateQCPassed {
		return PreservationManifest{}, ErrState
	}
	head, by = strings.TrimSpace(head), strings.TrimSpace(by)
	if head == "" || by == "" {
		return PreservationManifest{}, ErrInvalid
	}
	if !c.validTimeline() {
		return PreservationManifest{}, ErrIntegrity
	}
	index, indexErr := c.BuildGenerationEvidenceIndex()
	if indexErr != nil {
		return PreservationManifest{}, indexErr
	}
	ds := make([]string, len(c.Captures))
	for i, g := range c.Captures {
		ds[i] = g.AssetDigest
	}
	now := time.Now().UTC()
	payload := c.manifestPayload()
	return PreservationManifest{CaseID: c.ID, ManifestVersion: "1", CanonicalPayload: payload, CanonicalPayloadDigest: payloadDigest(payload), AuditHeadDigest: head, AuditRevision: c.Revision, CaptureDigests: ds, SealedBy: by, SealedAt: now, VerificationStatus: "VERIFIED", ComponentDigests: componentDigests(payload), GenerationEvidenceIndex: index}, nil
}

func (c *DigitizationCase) validTimeline() bool {
	if c.Assessment == nil || c.Plan == nil || len(c.PlanHistory) == 0 || c.PlanHistory[len(c.PlanHistory)-1].Fingerprint != c.Plan.Fingerprint || c.CurrentCaptureGeneration < 1 || len(c.Captures) < c.CurrentCaptureGeneration {
		return false
	}
	if facets, digest, err := NormalizeCarrierFacets(c.CarrierFacets); err != nil || stableDigest(facets) != stableDigest(c.CarrierFacets) || digest != c.CarrierFacetsDigest {
		return false
	}
	if c.IntakeReceipt != nil {
		receipt, err := NormalizeIntakeReceipt(*c.IntakeReceipt, time.Now().UTC())
		if err != nil || receipt.ReceiptDigest != c.IntakeReceipt.ReceiptDigest {
			return false
		}
	}
	if c.CustodyChainDigest != "" {
		events, custodian, location, digest, err := NormalizeCustodyEvents(c.CustodyEvents, c.IntakeReceipt, c.CreatedAt)
		if err != nil || stableDigest(events) != stableDigest(c.CustodyEvents) || custodian != c.CurrentCustodian || location != c.CurrentLocationCode || digest != c.CustodyChainDigest {
			return false
		}
	}
	if c.Assessment.DamageLocations != nil {
		locations, summaries, digest, err := normalizeDamageLocations(c.CarrierFacets, *c.Assessment)
		if err != nil || stableDigest(locations) != stableDigest(c.Assessment.DamageLocations) || stableDigest(summaries) != stableDigest(c.Assessment.DamageSummaries) || digest != c.Assessment.DamageLocationDigest {
			return false
		}
	}
	if c.Assessment.ObservationEvidence != nil {
		items, evidenceDigest, err := normalizeObservationEvidence(c.Assessment.ObservationEvidence, identifiedRiskCategories(*c.Assessment), c.Assessment.AssessedAt)
		if err != nil || stableDigest(items) != stableDigest(c.Assessment.ObservationEvidence) || evidenceDigest != c.Assessment.ObservationEvidenceDigest {
			return false
		}
	}
	for index, plan := range c.PlanHistory {
		if plan.Fingerprint != planFingerprint(plan) || (index > 0 && (plan.PlanRevision != c.PlanHistory[index-1].PlanRevision+1 || plan.RevisionReason == "" || !sameStrings(plan.ChangedFields, changedPlanFields(c.PlanHistory[index-1], plan)))) {
			return false
		}
		if plan.RiskControls != nil || plan.NoAdditionalControls {
			controls, covered, controlDigest, err := normalizeRiskControls(plan.RiskControls, plan.NoAdditionalControls, identifiedRiskCategories(*c.Assessment))
			if err != nil || stableDigest(controls) != stableDigest(plan.RiskControls) || !sameStrings(covered, plan.CoveredRiskCategories) || controlDigest != plan.RiskControlDigest {
				return false
			}
		}
		tasks, skipped, coverageDigest, total, err := normalizeCaptureTasks(c.CarrierFacets, plan.CaptureTasks, plan.SkippedFacets, plan.ChannelMap)
		if err != nil || stableDigest(tasks) != stableDigest(plan.CaptureTasks) || stableDigest(skipped) != stableDigest(plan.SkippedFacets) || coverageDigest != plan.TaskCoverageDigest || total != plan.EstimatedTotalDurationMs {
			return false
		}
	}
	digests := map[string]bool{}
	for _, capture := range c.Captures {
		windowMs := int64(capture.EndedAt.Sub(capture.StartedAt) / time.Millisecond)
		if capture.Generation < 1 || capture.Generation > c.CurrentCaptureGeneration || capture.PlanFingerprint == "" || strings.TrimSpace(capture.CaptureTaskID) == "" || strings.TrimSpace(capture.CalibrationReference) == "" || !capture.EndedAt.After(capture.StartedAt) || capture.DurationMs <= 0 || capture.DurationMs > windowMs || !validDigest(capture.AssetDigest) || capture.PeakDBFS > 0 || capture.PeakDBFS < -120 || digests[capture.AssetDigest] || capture.StartedAt.Before(capture.CalibratedAt) || capture.EndedAt.After(capture.CalibrationValidUntil) || capture.TechnicalEvidenceDigest != captureTechnicalDigest(capture) {
			return false
		}
		if capture.FixityChunks != nil {
			clone := capture
			if normalizeFixity(&clone) != nil || clone.FixityDigest != capture.FixityDigest {
				return false
			}
		}
		if capture.FileSegments != nil {
			clone := capture
			if normalizeFileSegments(&clone) != nil || stableDigest(clone.FileSegments) != stableDigest(capture.FileSegments) || clone.SegmentCombinationRule != capture.SegmentCombinationRule {
				return false
			}
		}
		if capture.CalibrationMeasurements != nil {
			clone := capture
			if capture.CalibrationProfile != nil {
				profile := *capture.CalibrationProfile
				clone.CalibrationProfile = &profile
			}
			if normalizeCalibrationEvidence(&clone, channelNames(c.Plan.ChannelMap)) != nil ||
				stableDigest(clone.CalibrationMeasurements) != stableDigest(capture.CalibrationMeasurements) ||
				stableDigest(clone.CalibrationResults) != stableDigest(capture.CalibrationResults) ||
				clone.CalibrationEvidenceDigest != capture.CalibrationEvidenceDigest ||
				clone.CalibrationStatus != capture.CalibrationStatus || clone.CalibrationPolicyVersion != capture.CalibrationPolicyVersion {
				return false
			}
		}
		if capture.OperationEvents != nil {
			events, paused, calculated, timelineDigest, err := normalizeOperationEvents(capture.OperationEvents, capture.StartedAt, capture.EndedAt, capture.DurationMs)
			if err != nil || stableDigest(events) != stableDigest(capture.OperationEvents) || paused != capture.PausedDurationMs || calculated != capture.CalculatedAudioDurationMs || timelineDigest != capture.OperationTimelineDigest {
				return false
			}
		}
		digests[capture.AssetDigest] = true
	}
	for _, quality := range c.Quality {
		if quality.ListeningIntervals == nil {
			continue
		}
		duration := int64(0)
		for _, capture := range c.Captures {
			if capture.Generation == quality.Generation {
				duration = capture.DurationMs
				break
			}
		}
		full := quality.Generation > 1 || (c.Assessment != nil && strings.EqualFold(c.Assessment.PlaybackRisk, "high"))
		intervals, coverage, digest, e := normalizeListeningIntervals(quality.ListeningIntervals, duration, channelNames(c.Plan.ChannelMap), full, quality.DefectMarkers)
		if e != nil {
			if detail, ok := e.(*DetailError); !ok || detail.Message != "人工听检覆盖不足" {
				return false
			}
		}
		if stableDigest(intervals) != stableDigest(quality.ListeningIntervals) || stableDigest(coverage) != stableDigest(quality.ListeningCoverage) || digest != quality.ListeningCoverageDigest {
			return false
		}
	}
	index, err := c.BuildGenerationEvidenceIndex()
	return err == nil && len(index) == len(c.Captures) && index[len(index)-1].QualityDecision == "PASS"
}

func remediationsCover(items []CategoryRemediation, required []string) bool {
	if len(items) != len(required) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item.Category] || strings.TrimSpace(item.Action) == "" || strings.TrimSpace(item.Owner) == "" || !validSummary(strings.TrimSpace(item.CompletionCriteria)) {
			return false
		}
		allowed := false
		for _, category := range required {
			if item.Category == category {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
		seen[item.Category] = true
	}
	return true
}

func sameMarkers(a, b []DefectMarker) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (c *DigitizationCase) Seal(m PreservationManifest) error {
	if c.State == StateSealed {
		return ErrSealed
	}
	if c.State != StateQCPassed {
		return ErrState
	}
	c.Manifest = &m
	c.State = StateSealed
	c.SealedAt = &m.SealedAt
	c.Revision++
	return nil
}
func (c *DigitizationCase) VerifyManifest() bool {
	if c.State != StateSealed || c.Manifest == nil || c.SealedAt == nil {
		return false
	}
	m := c.Manifest
	if m.CaseID != c.ID || m.ManifestVersion != "1" || m.AuditRevision != c.Revision-1 || m.SealedBy == "" || m.VerificationStatus != "VERIFIED" || !m.SealedAt.Equal(*c.SealedAt) {
		return false
	}
	if m.CanonicalPayloadDigest != payloadDigest(m.CanonicalPayload) || m.CanonicalPayloadDigest != payloadDigest(c.manifestPayload()) || len(m.CaptureDigests) != len(c.Captures) {
		return false
	}
	if stableDigest(m.GenerationEvidenceIndex) != stableDigest(m.CanonicalPayload.GenerationEvidenceIndex) {
		return false
	}
	verification := c.ManifestVerification()
	if !verification.Valid {
		return false
	}
	for i, capture := range c.Captures {
		if m.CaptureDigests[i] != capture.AssetDigest {
			return false
		}
	}
	return true
}
