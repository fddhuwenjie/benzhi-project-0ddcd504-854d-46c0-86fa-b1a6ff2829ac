package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

var custodyLocationRE = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._/-]{0,63}$`)

var custodySealStatuses = map[string]bool{"OPEN": true, "INTACT": true, "SEALED": true, "TAMPERED": true, "BROKEN": true, "UNSEALED": true}

// AppendCustodyTransfer validates and appends one hand-over to the current chain.
func (c *DigitizationCase) AppendCustodyTransfer(event CustodyEvent) error {
	if c.State == StateSealed {
		return ErrSealed
	}
	event.Transferor = strings.Join(strings.Fields(event.Transferor), " ")
	event.Receiver = strings.Join(strings.Fields(event.Receiver), " ")
	event.LocationCode = strings.ToUpper(strings.TrimSpace(event.LocationCode))
	event.SealStatus = strings.ToUpper(strings.TrimSpace(event.SealStatus))
	event.Notes = strings.TrimSpace(event.Notes)
	if event.Transferor == "" || event.Receiver == "" || strings.EqualFold(event.Transferor, event.Receiver) || event.Transferor != c.CurrentCustodian || event.OccurredAt.IsZero() || !custodyLocationRE.MatchString(event.LocationCode) || !custodySealStatuses[event.SealStatus] || !validSummary(event.Notes) {
		return Invalid("保管交接记录字段无效", map[string]interface{}{"field": "custody"})
	}
	event.OccurredAt = event.OccurredAt.UTC()
	if c.IntakeReceipt != nil && event.OccurredAt.Before(c.IntakeReceipt.ReceivedAt.UTC()) {
		return Invalid("保管交接时间早于入库回执", nil)
	}
	if n := len(c.CustodyEvents); n > 0 && !event.OccurredAt.After(c.CustodyEvents[n-1].OccurredAt) {
		return Invalid("保管交接时间必须晚于上一交接", nil)
	}
	c.CustodyEvents = append(c.CustodyEvents, event)
	c.CurrentCustodian, c.CurrentLocationCode = event.Receiver, event.LocationCode
	c.CustodyChainDigest = stableDigest(c.CustodyEvents)
	c.Revision++
	return nil
}

func NewCaseWithCustodyEvidence(id, accession, title, rights, carrier, scope string, intake *IntakeReceipt, facets []CarrierFacet, identifiers []AlternativeIdentifier, custody []CustodyEvent) (*DigitizationCase, error) {
	c, err := NewCaseWithAllEvidence(id, accession, title, rights, carrier, scope, intake, facets, identifiers)
	if err != nil {
		return nil, err
	}
	events, currentCustodian, currentLocation, digest, err := NormalizeCustodyEvents(custody, intake, c.CreatedAt)
	if err != nil {
		return nil, err
	}
	c.CustodyEvents = events
	c.CurrentCustodian = currentCustodian
	c.CurrentLocationCode = currentLocation
	if len(events) == 0 && intake != nil {
		c.CurrentCustodian = strings.TrimSpace(intake.Receiver)
	}
	c.CustodyChainDigest = digest
	return c, nil
}

func NormalizeCustodyEvents(items []CustodyEvent, receipt *IntakeReceipt, registeredAt time.Time) ([]CustodyEvent, string, string, string, error) {
	if len(items) == 0 {
		custodian := ""
		if receipt != nil {
			custodian = strings.TrimSpace(receipt.Receiver)
		}
		return []CustodyEvent{}, custodian, "", stableDigest([]CustodyEvent{}), nil
	}
	normalized := append([]CustodyEvent(nil), items...)
	locations := map[string]bool{}
	for i := range normalized {
		e := &normalized[i]
		e.Transferor = strings.Join(strings.Fields(e.Transferor), " ")
		e.Receiver = strings.Join(strings.Fields(e.Receiver), " ")
		e.LocationCode = strings.ToUpper(strings.TrimSpace(e.LocationCode))
		e.SealStatus = strings.ToUpper(strings.TrimSpace(e.SealStatus))
		e.Notes = strings.TrimSpace(e.Notes)
		if e.Transferor == "" || e.Receiver == "" || strings.EqualFold(e.Transferor, e.Receiver) || e.OccurredAt.IsZero() || !custodyLocationRE.MatchString(e.LocationCode) || !custodySealStatuses[e.SealStatus] || !validSummary(e.Notes) {
			return nil, "", "", "", Invalid("保管交接记录字段无效", map[string]interface{}{"item_index": i, "field": "custody_events"})
		}
		if locations[e.LocationCode] {
			return nil, "", "", "", Invalid("保管交接库位代码重复", map[string]interface{}{"item_index": i, "field": "location_code", "location_code": e.LocationCode})
		}
		locations[e.LocationCode] = true
		e.OccurredAt = e.OccurredAt.UTC()
		if e.OccurredAt.After(registeredAt) {
			return nil, "", "", "", Invalid("保管交接时间晚于建档时间", map[string]interface{}{"item_index": i, "field": "occurred_at", "occurred_at": e.OccurredAt, "registered_at": registeredAt})
		}
		if i > 0 {
			previous := normalized[i-1]
			if !e.OccurredAt.After(previous.OccurredAt) {
				return nil, "", "", "", Invalid("保管交接时间必须严格递增", map[string]interface{}{"item_index": i, "field": "occurred_at"})
			}
			if !strings.EqualFold(previous.Receiver, e.Transferor) {
				return nil, "", "", "", Invalid("相邻保管交接责任人不衔接", map[string]interface{}{"item_index": i, "field": "transferor", "expected_transferor": previous.Receiver})
			}
		}
	}
	if receipt != nil {
		if !strings.EqualFold(normalized[0].Transferor, strings.TrimSpace(receipt.Transferor)) || !strings.EqualFold(normalized[len(normalized)-1].Receiver, strings.TrimSpace(receipt.Receiver)) {
			return nil, "", "", "", Invalid("保管责任链与接收凭证双方不一致", map[string]interface{}{"expected_transferor": receipt.Transferor, "expected_receiver": receipt.Receiver})
		}
	}
	last := normalized[len(normalized)-1]
	return normalized, last.Receiver, last.LocationCode, stableDigest(normalized), nil
}

// CustodyChainDigest returns the deterministic digest for the persisted event order.
// It intentionally does not normalize or validate inputs, so callers can diagnose
// tampering by comparing this value with the stored digest.
func CustodyChainDigest(events []CustodyEvent) string { return stableDigest(events) }

func normalizeDamageLocations(facets []CarrierFacet, assessment ConditionAssessment) ([]DamageLocation, []DamageCategorySummary, string, error) {
	if assessment.DamageLocations == nil {
		return nil, nil, "", nil
	}
	facetSet := map[string]bool{}
	for _, facet := range facets {
		facetSet[facet.FacetID] = true
	}
	allowed := map[string]bool{"mold": true, "breakage": true, "adhesion": true, "contamination": true, "playback_risk": true}
	seen := map[string]bool{}
	normalized := append([]DamageLocation(nil), assessment.DamageLocations...)
	type aggregate struct {
		ratio           float64
		count, severity int
	}
	aggregates := map[string]aggregate{}
	for i := range normalized {
		item := &normalized[i]
		item.FacetID = strings.ToUpper(strings.TrimSpace(item.FacetID))
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		if item.Category == "playback" {
			item.Category = "playback_risk"
		}
		item.PhysicalLocation = strings.Join(strings.Fields(item.PhysicalLocation), " ")
		item.Severity = strings.ToUpper(strings.TrimSpace(item.Severity))
		item.ObservationNotes = strings.TrimSpace(item.ObservationNotes)
		item.EvidenceSummary = strings.TrimSpace(item.EvidenceSummary)
		if !facetSet[item.FacetID] {
			return nil, nil, "", Invalid("损伤定位引用不存在的分面", map[string]interface{}{"item_index": i, "field": "facet_id", "facet_id": item.FacetID})
		}
		if !allowed[item.Category] || item.PhysicalLocation == "" || severityRank(item.Severity) == 0 || !finite(item.AffectedRatio) || item.AffectedRatio <= 0 || item.AffectedRatio > 1 || !validSummary(item.ObservationNotes) || !validSummary(item.EvidenceSummary) {
			return nil, nil, "", Invalid("损伤定位字段无效", map[string]interface{}{"item_index": i, "field": "damage_locations"})
		}
		key := item.FacetID + "\x00" + strings.ToLower(item.PhysicalLocation)
		if seen[key] {
			return nil, nil, "", Invalid("同一分面物理位置重复", map[string]interface{}{"item_index": i, "facet_id": item.FacetID, "physical_location": item.PhysicalLocation})
		}
		seen[key] = true
		a := aggregates[item.Category]
		a.ratio += item.AffectedRatio
		a.count++
		if severityRank(item.Severity) > a.severity {
			a.severity = severityRank(item.Severity)
		}
		aggregates[item.Category] = a
	}
	has := func(category string) bool { _, ok := aggregates[category]; return ok }
	checks := []struct {
		category string
		declared bool
	}{
		{"mold", assessment.MoldLevel != "none"}, {"breakage", assessment.Breakage}, {"adhesion", assessment.Adhesion}, {"contamination", assessment.Contamination}, {"playback_risk", assessment.PlaybackRisk != "none"},
	}
	for _, check := range checks {
		if has(check.category) != check.declared {
			return nil, nil, "", Invalid("顶层风险声明与损伤定位不一致", map[string]interface{}{"category": check.category, "declared": check.declared, "has_location_evidence": has(check.category)})
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].FacetID != normalized[j].FacetID {
			return normalized[i].FacetID < normalized[j].FacetID
		}
		if normalized[i].PhysicalLocation != normalized[j].PhysicalLocation {
			return normalized[i].PhysicalLocation < normalized[j].PhysicalLocation
		}
		return normalized[i].Category < normalized[j].Category
	})
	categories := make([]string, 0, len(aggregates))
	for category := range aggregates {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	summaries := make([]DamageCategorySummary, 0, len(categories))
	for _, category := range categories {
		a := aggregates[category]
		summaries = append(summaries, DamageCategorySummary{Category: category, HighestSeverity: severityName(a.severity), TotalAffectedRatio: a.ratio, LocationCount: a.count})
	}
	digest := stableDigest(struct {
		Locations []DamageLocation        `json:"damage_locations"`
		Summaries []DamageCategorySummary `json:"damage_summaries"`
	}{normalized, summaries})
	return normalized, summaries, digest, nil
}

func severityRank(value string) int {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "MINOR":
		return 1
	case "MAJOR":
		return 2
	case "CRITICAL":
		return 3
	}
	return 0
}

func severityName(rank int) string {
	switch rank {
	case 1:
		return "MINOR"
	case 2:
		return "MAJOR"
	case 3:
		return "CRITICAL"
	}
	return ""
}

func SeverityAtLeast(value, minimum string) bool { return severityRank(value) >= severityRank(minimum) }

func validatePlanWindow(p *CapturePlan) error {
	if p.ScheduledStart.IsZero() && p.ScheduledEnd.IsZero() {
		return nil
	}
	if p.ScheduledStart.IsZero() || p.ScheduledEnd.IsZero() {
		return Invalid("预约起止时间必须同时提交", map[string]interface{}{"field": "scheduled_start"})
	}
	p.ScheduledStart, p.ScheduledEnd = p.ScheduledStart.UTC(), p.ScheduledEnd.UTC()
	if p.ScheduledStart.Before(p.ApprovedAt) || !p.ScheduledEnd.After(p.ScheduledStart) || p.ScheduledEnd.After(p.ValidUntil) {
		return Invalid("采集预约时窗不在方案有效期内", map[string]interface{}{"scheduled_start": p.ScheduledStart, "scheduled_end": p.ScheduledEnd, "approved_at": p.ApprovedAt, "valid_until": p.ValidUntil})
	}
	windowMs := p.ScheduledEnd.Sub(p.ScheduledStart).Milliseconds()
	if windowMs < p.EstimatedTotalDurationMs {
		return Invalid("采集预约时窗短于预计任务总时长", map[string]interface{}{"scheduled_duration_ms": windowMs, "estimated_total_duration_ms": p.EstimatedTotalDurationMs})
	}
	p.ReservationStatus = "ACTIVE"
	return nil
}

func (c *DigitizationCase) consumePlanReservation(at time.Time) {
	if c.Plan == nil || c.Plan.ScheduledStart.IsZero() || c.Plan.ReservationStatus == "CONSUMED" {
		return
	}
	now := time.Now().UTC()
	c.Plan.ReservationStatus, c.Plan.ReservationConsumedAt = "CONSUMED", &now
	for i := range c.PlanHistory {
		if c.PlanHistory[i].PlanRevision == c.Plan.PlanRevision {
			c.PlanHistory[i].ReservationStatus, c.PlanHistory[i].ReservationConsumedAt = "CONSUMED", &now
		}
	}
}

func normalizeFileSegments(g *CaptureGeneration) error {
	if g.FileSegments == nil {
		return nil
	}
	if len(g.FileSegments) == 0 || len(g.FileSegments) > 10000 {
		return Invalid("文件分段数量无效", map[string]interface{}{"field": "file_segments"})
	}
	segments := append([]FileSegment(nil), g.FileSegments...)
	digests := map[string]bool{}
	var totalDuration, totalSize int64
	h := sha256.New()
	for i := range segments {
		s := &segments[i]
		s.AssetDigest = strings.ToLower(strings.TrimSpace(s.AssetDigest))
		if s.Sequence != i+1 {
			return Invalid("文件分段序号必须从一开始连续", map[string]interface{}{"segment_index": i, "field": "segment_index", "expected_segment_index": i + 1})
		}
		if s.SourceStartMs < 0 || s.SourceEndMs <= s.SourceStartMs || s.DurationMs <= 0 || s.DurationMs != s.SourceEndMs-s.SourceStartMs || s.AssetSizeBytes <= 0 || !validDigest(s.AssetDigest) || digests[s.AssetDigest] || !s.StartsContinuous || !s.EndsContinuous {
			return Invalid("文件分段证据无效", map[string]interface{}{"segment_index": i, "field": "file_segments"})
		}
		if i == 0 && s.SourceStartMs != 0 {
			return Invalid("首段源位置必须从零开始", map[string]interface{}{"segment_index": i, "field": "source_start_ms"})
		}
		if i > 0 && s.SourceStartMs != segments[i-1].SourceEndMs {
			return Invalid("相邻文件分段源位置不连续", map[string]interface{}{"segment_index": i, "field": "source_start_ms", "expected_source_start_ms": segments[i-1].SourceEndMs})
		}
		digests[s.AssetDigest] = true
		decoded, _ := hex.DecodeString(s.AssetDigest)
		_, _ = h.Write(decoded)
		totalDuration += s.DurationMs
		totalSize += s.AssetSizeBytes
	}
	combined := hex.EncodeToString(h.Sum(nil))
	if totalDuration != g.DurationMs || segments[len(segments)-1].SourceEndMs != g.DurationMs || totalSize != g.AssetSizeBytes || !strings.EqualFold(combined, g.AssetDigest) {
		return Invalid("任务级资产汇总与文件分段复算结果不一致", map[string]interface{}{"calculated_duration_ms": totalDuration, "calculated_asset_size_bytes": totalSize, "calculated_asset_digest": combined})
	}
	windowMs := g.EndedAt.Sub(g.StartedAt).Milliseconds()
	if segments[len(segments)-1].SourceEndMs > windowMs {
		return Invalid("文件分段证据超出采集时间窗", map[string]interface{}{"segment_index": len(segments) - 1, "capture_window_ms": windowMs})
	}
	g.FileSegments = segments
	g.SegmentCombinationRule = "sha256(concat(binary_segment_asset_digests))"
	return nil
}

func calculateDefectImpacts(markers []DefectMarker, duration int64, channels []string) ([]DefectMarker, []DefectImpact, string, string, error) {
	allowedChannels := map[string]bool{}
	for _, channel := range channels {
		allowedChannels[strings.ToUpper(channel)] = true
	}
	normalized := append([]DefectMarker(nil), markers...)
	for i := range normalized {
		m := &normalized[i]
		m.DefectType = strings.ToLower(strings.TrimSpace(m.DefectType))
		if m.DefectType == "listening" {
			m.DefectType = "listening_anomaly"
		}
		m.Channel = strings.ToUpper(strings.TrimSpace(m.Channel))
		m.Description = strings.TrimSpace(m.Description)
		m.Severity = strings.ToUpper(strings.TrimSpace(m.Severity))
		if m.Severity == "" {
			m.Severity = "MAJOR"
		}
		if (m.DefectType != "clipping" && m.DefectType != "dropout" && m.DefectType != "listening_anomaly") || !allowedChannels[m.Channel] || severityRank(m.Severity) == 0 || m.PositionMs < 0 || m.PositionMs >= duration || m.DurationMs <= 0 || m.DurationMs > duration-m.PositionMs || !validSummary(m.Description) {
			return nil, nil, "", "", Invalid("缺陷标记无效", map[string]interface{}{"marker_index": i})
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].DefectType != normalized[j].DefectType {
			return normalized[i].DefectType < normalized[j].DefectType
		}
		if normalized[i].Channel != normalized[j].Channel {
			return normalized[i].Channel < normalized[j].Channel
		}
		if normalized[i].PositionMs != normalized[j].PositionMs {
			return normalized[i].PositionMs < normalized[j].PositionMs
		}
		return normalized[i].DurationMs < normalized[j].DurationMs
	})
	impacts := []DefectImpact{}
	for start := 0; start < len(normalized); {
		end := start
		keyType, keyChannel := normalized[start].DefectType, normalized[start].Channel
		maxEnd, affected, maxSeverity := int64(-1), int64(0), 0
		for end < len(normalized) && normalized[end].DefectType == keyType && normalized[end].Channel == keyChannel {
			markerEnd := normalized[end].PositionMs + normalized[end].DurationMs
			if normalized[end].PositionMs >= maxEnd {
				affected += normalized[end].DurationMs
			} else if markerEnd > maxEnd {
				affected += markerEnd - maxEnd
			}
			if markerEnd > maxEnd {
				maxEnd = markerEnd
			}
			if severityRank(normalized[end].Severity) > maxSeverity {
				maxSeverity = severityRank(normalized[end].Severity)
			}
			end++
		}
		impacts = append(impacts, DefectImpact{DefectType: keyType, Channel: keyChannel, AffectedDurationMs: affected, AffectedRatio: float64(affected) / float64(duration), MarkerCount: end - start, HighestSeverity: severityName(maxSeverity)})
		start = end
	}
	markerDigest := stableDigest(normalized)
	impactDigest := stableDigest(struct {
		Markers []DefectMarker `json:"defect_markers"`
		Impacts []DefectImpact `json:"defect_impacts"`
	}{normalized, impacts})
	return normalized, impacts, markerDigest, impactDigest, nil
}

func normalizeCompletedRemediations(items []CategoryRemediation, required []string, failed QualityDecision, planOperator, authorizedBy string, authorizedAt time.Time) ([]CategoryRemediation, string, error) {
	seenCategory, seenEvidence := map[string]bool{}, map[string]bool{}
	requiredSet := map[string]bool{}
	for _, category := range required {
		requiredSet[category] = true
	}
	normalized := append([]CategoryRemediation(nil), items...)
	for i := range normalized {
		item := &normalized[i]
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		item.Action, item.Owner, item.CompletionCriteria = strings.TrimSpace(item.Action), strings.TrimSpace(item.Owner), strings.TrimSpace(item.CompletionCriteria)
		item.PerformedBy, item.Result = strings.TrimSpace(item.PerformedBy), strings.TrimSpace(item.Result)
		item.EvidenceDigest = strings.ToLower(strings.TrimSpace(item.EvidenceDigest))
		item.VerificationMethod = strings.TrimSpace(item.VerificationMethod)
		if !requiredSet[item.Category] || seenCategory[item.Category] {
			return nil, "", Invalid("整改分类必须准确覆盖全部质量失败", map[string]interface{}{"item_index": i, "category": item.Category})
		}
		seenCategory[item.Category] = true
		legacy := item.PerformedBy == "" && item.CompletedAt.IsZero() && item.Result == "" && item.EvidenceDigest == "" && item.VerificationMethod == ""
		if legacy {
			if item.Action == "" || item.Owner == "" || !validSummary(item.CompletionCriteria) {
				return nil, "", Invalid("分类整改项无效", map[string]interface{}{"item_index": i})
			}
			continue
		}
		if item.Action == "" || item.Owner == "" || !validSummary(item.CompletionCriteria) || item.PerformedBy == "" || item.CompletedAt.IsZero() || item.Result == "" || !validDigest(item.EvidenceDigest) || !validSummary(item.VerificationMethod) || !item.CompletedAt.After(failed.ReviewedAt) || item.CompletedAt.After(authorizedAt) {
			return nil, "", Invalid("整改完成证据无效", map[string]interface{}{"item_index": i, "category": item.Category})
		}
		if seenEvidence[item.EvidenceDigest] {
			return nil, "", Invalid("整改证据摘要重复", map[string]interface{}{"item_index": i, "evidence_digest": item.EvidenceDigest})
		}
		seenEvidence[item.EvidenceDigest] = true
		for _, prohibited := range []string{planOperator, failed.Reviewer, authorizedBy} {
			if strings.EqualFold(item.PerformedBy, strings.TrimSpace(prohibited)) {
				return nil, "", Invalid("整改执行人不满足人员分离要求", map[string]interface{}{"item_index": i, "performed_by": item.PerformedBy})
			}
		}
		item.CompletedAt = item.CompletedAt.UTC()
	}
	missing := []string{}
	for _, category := range required {
		if !seenCategory[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 || len(normalized) != len(required) {
		return nil, "", Invalid("整改分类必须准确覆盖全部质量失败", map[string]interface{}{"missing_categories": missing, "required_categories": required})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Category < normalized[j].Category })
	return normalized, stableDigest(normalized), nil
}
