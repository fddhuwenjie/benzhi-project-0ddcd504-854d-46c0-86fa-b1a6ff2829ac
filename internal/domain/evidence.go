package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

func stableDigest(v interface{}) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func NormalizeIntakeReceipt(receipt IntakeReceipt, now time.Time) (IntakeReceipt, error) {
	receipt.TransferOrganization = strings.TrimSpace(receipt.TransferOrganization)
	receipt.Transferor = strings.TrimSpace(receipt.Transferor)
	receipt.Receiver = strings.TrimSpace(receipt.Receiver)
	receipt.BatchNumber = strings.ToUpper(strings.TrimSpace(receipt.BatchNumber))
	receipt.PackagingCondition = strings.TrimSpace(receipt.PackagingCondition)
	values := []string{receipt.TransferOrganization, receipt.Transferor, receipt.Receiver, receipt.BatchNumber, receipt.PackagingCondition}
	for _, value := range values {
		if len([]rune(value)) < 1 || len([]rune(value)) > 200 {
			return IntakeReceipt{}, Invalid("交接凭证必填文本长度无效", nil)
		}
	}
	if receipt.ReceivedAt.IsZero() || receipt.ReceivedAt.After(now.UTC()) {
		return IntakeReceipt{}, Invalid("接收时间不得晚于当前时间", nil)
	}
	if strings.EqualFold(receipt.Transferor, receipt.Receiver) {
		return IntakeReceipt{}, Invalid("移交人与接收人不得相同", nil)
	}
	receipt.ReceivedAt = receipt.ReceivedAt.UTC()
	receipt.ReceiptDigest = ""
	receipt.ReceiptDigest = stableDigest(receipt)
	return receipt, nil
}

func identifiedRiskCategories(a ConditionAssessment) []string {
	result := []string{}
	if strings.ToLower(a.MoldLevel) != "none" {
		result = append(result, "mold")
	}
	if a.Breakage {
		result = append(result, "breakage")
	}
	if a.Adhesion {
		result = append(result, "adhesion")
	}
	if a.Contamination {
		result = append(result, "contamination")
	}
	if !strings.EqualFold(a.PlaybackRisk, "none") {
		result = append(result, "playback")
	}
	return result
}

func normalizeObservationEvidence(items []ObservationEvidence, required []string, submittedAt time.Time) ([]ObservationEvidence, string, error) {
	requiredSet := map[string]bool{}
	for _, category := range required {
		requiredSet[category] = true
	}
	// 资产摘要只禁止在同一风险类别内重复引用；同一份照片/扫描可
	// 合法地同时证明多个风险类别。
	seenDigest, covered := map[string]bool{}, map[string]bool{}
	normalized := append([]ObservationEvidence{}, items...)
	for i := range normalized {
		item := &normalized[i]
		item.RiskCategory = strings.ToLower(strings.TrimSpace(item.RiskCategory))
		if item.RiskCategory == "playback_risk" {
			item.RiskCategory = "playback"
		}
		item.EvidenceType = strings.ToLower(strings.TrimSpace(item.EvidenceType))
		item.AssetDigest = strings.ToLower(strings.TrimSpace(item.AssetDigest))
		item.RecordedBy = strings.TrimSpace(item.RecordedBy)
		item.Description = strings.TrimSpace(item.Description)
		if !requiredSet[item.RiskCategory] {
			return nil, "", Invalid("观察证据指向未声明风险", map[string]interface{}{"risk_category": item.RiskCategory})
		}
		key := item.RiskCategory + "\x00" + item.AssetDigest
		if !validDigest(item.AssetDigest) || seenDigest[key] {
			return nil, "", Invalid("观察证据资产摘要无效或重复", map[string]interface{}{"item_index": i})
		}
		if item.EvidenceType == "" || item.RecordedBy == "" || !validSummary(item.Description) || item.ObservedAt.IsZero() || item.ObservedAt.After(submittedAt) {
			return nil, "", Invalid("观察证据字段无效", map[string]interface{}{"item_index": i})
		}
		item.ObservedAt = item.ObservedAt.UTC()
		seenDigest[key], covered[item.RiskCategory] = true, true
	}
	missing := []string{}
	for _, category := range required {
		if !covered[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 {
		return nil, "", Invalid("观察证据未覆盖全部风险", map[string]interface{}{"missing_categories": missing})
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].RiskCategory != normalized[j].RiskCategory {
			return normalized[i].RiskCategory < normalized[j].RiskCategory
		}
		return normalized[i].AssetDigest < normalized[j].AssetDigest
	})
	return normalized, stableDigest(normalized), nil
}

func normalizeRiskControls(items []RiskControl, noAdditional bool, required []string) ([]RiskControl, []string, string, error) {
	if len(required) == 0 {
		if !noAdditional || len(items) != 0 {
			return nil, nil, "", Invalid("无评估风险时必须声明无需附加控制且控制清单为空", nil)
		}
		return []RiskControl{}, []string{}, stableDigest([]RiskControl{}), nil
	}
	if noAdditional {
		return nil, nil, "", Invalid("存在评估风险时不得声明无需附加控制", map[string]interface{}{"required_categories": required})
	}
	requiredSet, seen := map[string]bool{}, map[string]bool{}
	for _, category := range required {
		requiredSet[category] = true
	}
	normalized := append([]RiskControl(nil), items...)
	for i := range normalized {
		item := &normalized[i]
		item.RiskCategory = strings.ToLower(strings.TrimSpace(item.RiskCategory))
		if item.RiskCategory == "playback_risk" {
			item.RiskCategory = "playback"
		}
		item.ControlCategory = strings.ToLower(strings.TrimSpace(item.ControlCategory))
		item.OperationalMeasure = strings.TrimSpace(item.OperationalMeasure)
		item.ResponsiblePerson = strings.TrimSpace(item.ResponsiblePerson)
		item.PreCaptureCheck = strings.TrimSpace(item.PreCaptureCheck)
		if !requiredSet[item.RiskCategory] || seen[item.RiskCategory] {
			return nil, nil, "", Invalid("风险控制类别重复或超出评估风险", map[string]interface{}{"risk_category": item.RiskCategory})
		}
		if item.ControlCategory == "" || item.OperationalMeasure == "" || item.ResponsiblePerson == "" || !validSummary(item.PreCaptureCheck) {
			return nil, nil, "", Invalid("风险控制项字段无效", map[string]interface{}{"item_index": i})
		}
		seen[item.RiskCategory] = true
	}
	missing := []string{}
	for _, category := range required {
		if !seen[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 {
		return nil, nil, "", Invalid("风险控制未覆盖全部评估风险", map[string]interface{}{"missing_categories": missing})
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].RiskCategory != normalized[j].RiskCategory {
			return normalized[i].RiskCategory < normalized[j].RiskCategory
		}
		return normalized[i].ControlCategory < normalized[j].ControlCategory
	})
	covered := make([]string, len(normalized))
	for i := range normalized {
		covered[i] = normalized[i].RiskCategory
	}
	return normalized, covered, stableDigest(normalized), nil
}

func normalizeOperationEvents(items []OperationEvent, started, ended time.Time, durationMs int64) ([]OperationEvent, int64, int64, string, error) {
	normalized := append([]OperationEvent(nil), items...)
	allowed := map[string]bool{"pause": true, "resume": true, "carrier_cleaning": true, "splice_repair": true, "operator_note": true}
	for i := range normalized {
		event := &normalized[i]
		event.Type = strings.ToLower(strings.TrimSpace(event.Type))
		event.Operator = strings.TrimSpace(event.Operator)
		event.Description = strings.TrimSpace(event.Description)
		if !allowed[event.Type] || event.OccurredAt.Before(started) || event.OccurredAt.After(ended) || event.Operator == "" || !validSummary(event.Description) {
			return nil, 0, 0, "", Invalid("采集操作事件无效", map[string]interface{}{"item_index": i})
		}
		event.OccurredAt = event.OccurredAt.UTC()
	}
	rank := map[string]int{"pause": 0, "carrier_cleaning": 1, "splice_repair": 1, "operator_note": 1, "resume": 2}
	sort.Slice(normalized, func(i, j int) bool {
		if !normalized[i].OccurredAt.Equal(normalized[j].OccurredAt) {
			return normalized[i].OccurredAt.Before(normalized[j].OccurredAt)
		}
		if rank[normalized[i].Type] != rank[normalized[j].Type] {
			return rank[normalized[i].Type] < rank[normalized[j].Type]
		}
		if normalized[i].Type != normalized[j].Type {
			return normalized[i].Type < normalized[j].Type
		}
		if normalized[i].Operator != normalized[j].Operator {
			return normalized[i].Operator < normalized[j].Operator
		}
		return normalized[i].Description < normalized[j].Description
	})
	var pauseAt *time.Time
	pausedMs := int64(0)
	for i := range normalized {
		event := normalized[i]
		switch event.Type {
		case "pause":
			if pauseAt != nil {
				return nil, 0, 0, "", Invalid("暂停事件不得嵌套", nil)
			}
			t := event.OccurredAt
			pauseAt = &t
		case "resume":
			if pauseAt == nil || !event.OccurredAt.After(*pauseAt) {
				return nil, 0, 0, "", Invalid("恢复事件缺少有效暂停事件", nil)
			}
			pausedMs += event.OccurredAt.Sub(*pauseAt).Milliseconds()
			pauseAt = nil
		case "carrier_cleaning", "splice_repair":
			if pauseAt == nil {
				return nil, 0, 0, "", Invalid("载体干预必须发生在暂停区间", map[string]interface{}{"item_index": i})
			}
		}
	}
	if pauseAt != nil {
		return nil, 0, 0, "", Invalid("暂停事件缺少对应恢复事件", nil)
	}
	netMs := ended.Sub(started).Milliseconds() - pausedMs
	tolerance := int64(math.Max(1000, float64(netMs)/100))
	if abs64(durationMs-netMs) > tolerance {
		return nil, 0, 0, "", Invalid("音频时长与扣除暂停后的净采集时长不一致", map[string]interface{}{"calculated_audio_duration_ms": netMs, "allowed_error_ms": tolerance})
	}
	return normalized, pausedMs, netMs, stableDigest(normalized), nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func channelNames(channelMap string) []string {
	parts := strings.FieldsFunc(channelMap, func(r rune) bool { return r == '/' || r == ',' || r == ';' || r == '|' })
	result := make([]string, len(parts))
	for i := range parts {
		result[i] = strings.ToUpper(strings.TrimSpace(parts[i]))
	}
	return result
}

func normalizeListeningIntervals(items []ListeningInterval, duration int64, channels []string, full bool, markers []DefectMarker) ([]ListeningInterval, []ChannelCoverage, string, error) {
	validChannels := map[string]bool{}
	for _, channel := range channels {
		validChannels[channel] = true
	}
	normalized := append([]ListeningInterval(nil), items...)
	for i := range normalized {
		item := &normalized[i]
		item.Channel = strings.ToUpper(strings.TrimSpace(item.Channel))
		item.Method = strings.ToLower(strings.TrimSpace(item.Method))
		if !validChannels[item.Channel] || item.StartMs < 0 || item.EndMs <= item.StartMs || item.EndMs > duration || item.Method == "" {
			return nil, nil, "", Invalid("听检区间无效", map[string]interface{}{"item_index": i})
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Channel != normalized[j].Channel {
			return normalized[i].Channel < normalized[j].Channel
		}
		if normalized[i].StartMs != normalized[j].StartMs {
			return normalized[i].StartMs < normalized[j].StartMs
		}
		return normalized[i].EndMs < normalized[j].EndMs
	})
	merged := map[string][][2]int64{}
	mergedMethod := map[string][]string{}
	for _, item := range normalized {
		segments := merged[item.Channel]
		if len(segments) > 0 && item.StartMs <= segments[len(segments)-1][1] {
			if item.EndMs > segments[len(segments)-1][1] {
				segments[len(segments)-1][1] = item.EndMs
			}
		} else {
			segments = append(segments, [2]int64{item.StartMs, item.EndMs})
			methods := mergedMethod[item.Channel]
			methods = append(methods, item.Method)
			mergedMethod[item.Channel] = methods
		}
		merged[item.Channel] = segments
	}
	// 将重叠区间规范化为不重叠区间，保证查询和摘要稳定；合并后沿用
	// 合并段首条记录的听检方法。
	canonicalIntervals := make([]ListeningInterval, 0, len(normalized))
	for _, channel := range channels {
		segments := merged[channel]
		methods := mergedMethod[channel]
		for i, segment := range segments {
			method := ""
			if i < len(methods) {
				method = methods[i]
			}
			canonicalIntervals = append(canonicalIntervals, ListeningInterval{StartMs: segment[0], EndMs: segment[1], Channel: channel, Method: method})
		}
	}
	sort.Slice(canonicalIntervals, func(i, j int) bool {
		if canonicalIntervals[i].Channel != canonicalIntervals[j].Channel {
			return canonicalIntervals[i].Channel < canonicalIntervals[j].Channel
		}
		return canonicalIntervals[i].StartMs < canonicalIntervals[j].StartMs
	})
	coverage := make([]ChannelCoverage, 0, len(channels))
	missing := []string{}
	for _, channel := range channels {
		covered := int64(0)
		for _, segment := range merged[channel] {
			covered += segment[1] - segment[0]
		}
		ratio := float64(covered) / float64(duration)
		ok := covered >= duration
		if !full {
			begin, middle, end := false, false, false
			for _, segment := range merged[channel] {
				begin = begin || segment[0] == 0
				middle = middle || (segment[0] <= duration/2 && segment[1] > duration/2)
				end = end || segment[1] == duration
			}
			ok = ratio >= 0.10 && begin && middle && end
		}
		if !ok {
			missing = append(missing, channel)
		}
		coverage = append(coverage, ChannelCoverage{Channel: channel, CoveredMs: covered, CoverageRatio: ratio})
	}
	coverageDigest := stableDigest(struct {
		Intervals []ListeningInterval `json:"intervals"`
		Coverage  []ChannelCoverage   `json:"coverage"`
	}{canonicalIntervals, coverage})
	if len(missing) > 0 {
		// 返回规范化结果和覆盖摘要，同时报告门禁失败；应用层可据此
		// 生成 RECAPTURE_REQUIRED 的质量决定并保留缺失信息。
		return canonicalIntervals, coverage, coverageDigest, Invalid("人工听检覆盖不足", map[string]interface{}{"missing_channels": missing, "full_coverage_required": full})
	}
	for _, marker := range markers {
		if marker.DefectType != "listening_anomaly" {
			continue
		}
		inside := false
		for _, segment := range merged[strings.ToUpper(marker.Channel)] {
			if marker.PositionMs >= segment[0] && marker.PositionMs+marker.DurationMs <= segment[1] {
				inside = true
				break
			}
		}
		if !inside {
			return nil, nil, "", Invalid("人工听检异常标记不在已听检区间内", map[string]interface{}{"position_ms": marker.PositionMs, "channel": marker.Channel})
		}
	}
	return canonicalIntervals, coverage, coverageDigest, nil
}

func normalizeRemediationChecks(items []RemediationCheck, authorization RecaptureAction, failures []string) ([]RemediationCheck, []string, []string, []string, string, error) {
	failureSet, required, owners := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, category := range failures {
		failureSet[category] = true
	}
	for _, item := range authorization.Remediations {
		required[item.Category], owners[item.Category] = true, item.Owner
	}
	normalized := append([]RemediationCheck(nil), items...)
	seen := map[string]bool{}
	for i := range normalized {
		item := &normalized[i]
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		item.Result = strings.ToLower(strings.TrimSpace(item.Result))
		item.VerifiedBy = strings.TrimSpace(item.VerifiedBy)
		item.EvidenceDescription = strings.TrimSpace(item.EvidenceDescription)
		if item.Result == "已解决" {
			item.Result = "resolved"
		}
		if item.Result == "持续存在" {
			item.Result = "persistent"
		}
		if !required[item.Category] || seen[item.Category] || (item.Result != "resolved" && item.Result != "persistent") || item.VerifiedBy == "" || strings.EqualFold(item.VerifiedBy, owners[item.Category]) || !validSummary(item.EvidenceDescription) {
			return nil, nil, nil, nil, "", Invalid("整改成效核验项无效", map[string]interface{}{"item_index": i, "category": item.Category})
		}
		expected := "resolved"
		if failureSet[item.Category] {
			expected = "persistent"
		}
		if item.Result != expected {
			return nil, nil, nil, nil, "", Invalid("整改核验结果与本代质量失败不一致", map[string]interface{}{"category": item.Category, "expected_result": expected})
		}
		seen[item.Category] = true
	}
	missing := []string{}
	for category := range required {
		if !seen[category] {
			missing = append(missing, category)
		}
	}
	if len(missing) > 0 || len(normalized) != len(required) {
		sort.Strings(missing)
		return nil, nil, nil, nil, "", Invalid("整改成效核验未覆盖上一授权", map[string]interface{}{"missing_categories": missing})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Category < normalized[j].Category })
	resolved, persistent, newly := []string{}, []string{}, []string{}
	for _, item := range normalized {
		if item.Result == "resolved" {
			resolved = append(resolved, item.Category)
		} else {
			persistent = append(persistent, item.Category)
		}
	}
	for _, category := range failures {
		if !required[category] {
			newly = append(newly, category)
		}
	}
	return normalized, resolved, persistent, newly, stableDigest(normalized), nil
}

type registrationManifestComponent struct {
	ID                          string                  `json:"id"`
	AccessionCode               string                  `json:"accession_code"`
	AlternativeIdentifiers      []AlternativeIdentifier `json:"alternative_identifiers"`
	AlternativeIdentifierDigest string                  `json:"alternative_identifier_digest"`
	Title                       string                  `json:"title"`
	RightsNote                  string                  `json:"rights_note"`
	CarrierType                 string                  `json:"carrier_type"`
	ContentScope                string                  `json:"content_scope"`
	IntakeReceipt               *IntakeReceipt          `json:"intake_receipt"`
	CarrierFacets               []CarrierFacet          `json:"carrier_facets"`
	CarrierFacetsDigest         string                  `json:"carrier_facets_digest"`
	CustodyEvents               []CustodyEvent          `json:"custody_events"`
	CurrentCustodian            string                  `json:"current_custodian"`
	CurrentLocationCode         string                  `json:"current_location_code"`
	CustodyChainDigest          string                  `json:"custody_chain_digest"`
}
type plansManifestComponent struct {
	Current *CapturePlan  `json:"current"`
	History []CapturePlan `json:"history"`
}
type assessmentManifestComponent struct {
	Current *ConditionAssessment  `json:"current"`
	History []ConditionAssessment `json:"history"`
}

func registrationComponent(payload ManifestPayload) registrationManifestComponent {
	return registrationManifestComponent{ID: payload.ID, AccessionCode: payload.AccessionCode, AlternativeIdentifiers: payload.AlternativeIdentifiers, AlternativeIdentifierDigest: payload.AlternativeIdentifierDigest, Title: payload.Title, RightsNote: payload.RightsNote, CarrierType: payload.CarrierType, ContentScope: payload.ContentScope, IntakeReceipt: payload.IntakeReceipt, CarrierFacets: payload.CarrierFacets, CarrierFacetsDigest: payload.CarrierFacetsDigest, CustodyEvents: payload.CustodyEvents, CurrentCustodian: payload.CurrentCustodian, CurrentLocationCode: payload.CurrentLocationCode, CustodyChainDigest: payload.CustodyChainDigest}
}
func plansComponent(payload ManifestPayload) plansManifestComponent {
	return plansManifestComponent{payload.Plan, payload.PlanHistory}
}
func assessmentComponent(payload ManifestPayload) assessmentManifestComponent {
	return assessmentManifestComponent{payload.Assessment, payload.AssessmentHistory}
}

func componentDigests(payload ManifestPayload) ComponentDigests {
	registration, plans, assessment := registrationComponent(payload), plansComponent(payload), assessmentComponent(payload)
	return ComponentDigests{Registration: stableDigest(registration), Assessment: stableDigest(assessment), Plans: stableDigest(plans), Captures: stableDigest(payload.Captures), Quality: stableDigest(payload.Quality), Recaptures: stableDigest(payload.Recaptures)}
}

func (c *DigitizationCase) ManifestVerification() ManifestVerification {
	result := ManifestVerification{Status: "INVALID", MismatchedComponents: []string{}, ReferenceErrors: []EvidenceReferenceError{}}
	if c.Manifest == nil {
		result.MismatchedComponents = append(result.MismatchedComponents, "manifest")
		return result
	}
	builtIndex, indexErr := c.BuildGenerationEvidenceIndex()
	if indexErr != nil {
		reference := EvidenceReferenceError{Field: "generation_evidence_index", Message: indexErr.Error()}
		if detail, ok := indexErr.(*DetailError); ok {
			if value, exists := detail.Details["generation"].(int); exists {
				reference.Generation = value
			}
			if value, exists := detail.Details["field"].(string); exists {
				reference.Field = value
			}
		}
		result.ReferenceErrors = append(result.ReferenceErrors, reference)
		result.MismatchedComponents = append(result.MismatchedComponents, "generation_evidence_index")
	} else if stableDigest(builtIndex) != stableDigest(c.Manifest.GenerationEvidenceIndex) || stableDigest(c.Manifest.GenerationEvidenceIndex) != stableDigest(c.Manifest.CanonicalPayload.GenerationEvidenceIndex) {
		result.ReferenceErrors = append(result.ReferenceErrors, EvidenceReferenceError{Field: "generation_evidence_index", Message: "保存包代次证据关系索引不匹配"})
		result.MismatchedComponents = append(result.MismatchedComponents, "generation_evidence_index")
	}
	expected := c.Manifest.ComponentDigests
	manifestActual, caseActual := componentDigests(c.Manifest.CanonicalPayload), componentDigests(c.manifestPayload())
	checks := []struct{ name, expected, manifestActual, caseActual string }{{"registration", expected.Registration, manifestActual.Registration, caseActual.Registration}, {"assessment", expected.Assessment, manifestActual.Assessment, caseActual.Assessment}, {"plans", expected.Plans, manifestActual.Plans, caseActual.Plans}, {"captures", expected.Captures, manifestActual.Captures, caseActual.Captures}, {"quality", expected.Quality, manifestActual.Quality, caseActual.Quality}, {"recaptures", expected.Recaptures, manifestActual.Recaptures, caseActual.Recaptures}}
	for _, check := range checks {
		if check.expected != check.manifestActual || check.expected != check.caseActual {
			result.MismatchedComponents = append(result.MismatchedComponents, check.name)
		}
	}
	result.ExpectedDigest = c.Manifest.CanonicalPayloadDigest
	manifestDigest, caseDigest := payloadDigest(c.Manifest.CanonicalPayload), payloadDigest(c.manifestPayload())
	result.ActualDigest = manifestDigest
	if manifestDigest == result.ExpectedDigest && caseDigest != result.ExpectedDigest {
		result.ActualDigest = caseDigest
	}
	result.Valid = len(result.MismatchedComponents) == 0 && result.ExpectedDigest == manifestDigest && result.ExpectedDigest == caseDigest
	if result.Valid {
		result.Status = "VERIFIED"
	}
	return result
}
