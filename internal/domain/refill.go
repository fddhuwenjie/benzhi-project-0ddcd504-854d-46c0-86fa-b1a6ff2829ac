package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
)

func NormalizeCarrierFacets(items []CarrierFacet) ([]CarrierFacet, string, error) {
	if len(items) == 0 || len(items) > 100 {
		return nil, "", Invalid("载体至少包含一个分面", map[string]interface{}{"field": "carrier_facets"})
	}
	normalized := append([]CarrierFacet(nil), items...)
	ids, orders, playable := map[string]bool{}, map[int]bool{}, false
	for i := range normalized {
		item := &normalized[i]
		item.FacetID = strings.ToUpper(strings.TrimSpace(item.FacetID))
		item.Label = strings.TrimSpace(item.Label)
		item.ContentScope = strings.TrimSpace(item.ContentScope)
		if item.FacetID == "" || item.Label == "" || item.ContentScope == "" || item.PhysicalOrder < 1 {
			return nil, "", Invalid("载体分面字段无效", map[string]interface{}{"facet_id": item.FacetID, "item_index": i})
		}
		if ids[item.FacetID] {
			return nil, "", Invalid("分面编号重复", map[string]interface{}{"facet_id": item.FacetID})
		}
		if orders[item.PhysicalOrder] {
			return nil, "", Invalid("分面物理序号重复", map[string]interface{}{"facet_id": item.FacetID, "physical_order": item.PhysicalOrder})
		}
		ids[item.FacetID], orders[item.PhysicalOrder] = true, true
		playable = playable || item.Playable
	}
	for order := 1; order <= len(normalized); order++ {
		if !orders[order] {
			return nil, "", Invalid("分面物理序号必须从一开始连续", map[string]interface{}{"missing_physical_order": order})
		}
	}
	if !playable {
		return nil, "", Invalid("全部分面均不可播放", map[string]interface{}{"field": "carrier_facets.playable"})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].PhysicalOrder < normalized[j].PhysicalOrder })
	return normalized, stableDigest(normalized), nil
}

func normalizeAcclimatization(value *Acclimatization, carrier string, assessment ConditionAssessment, assessedAt time.Time) (*Acclimatization, error) {
	autoRequired := assessment.Contamination || strings.Contains(strings.ToLower(carrier), "wet") || strings.Contains(strings.ToLower(carrier), "damp") || strings.Contains(carrier, "受潮") || strings.Contains(carrier, "低温") || strings.Contains(carrier, "冷冻")
	if value == nil {
		// 旧登记仍可继续流转；显式受潮、低温等载体必须提交稳定化证据。
		if autoRequired && (strings.Contains(strings.ToLower(carrier), "wet") || strings.Contains(strings.ToLower(carrier), "damp") || strings.Contains(carrier, "受潮") || strings.Contains(carrier, "低温") || strings.Contains(carrier, "冷冻")) {
			return nil, Invalid("该载体需要播放前环境稳定化证据", map[string]interface{}{"field": "acclimatization"})
		}
		return nil, nil
	}
	n := *value
	n.Required = n.Required || autoRequired
	n.ReleaseReasons = []string{}
	n.AbnormalReadings = []AcclimatizationReading{}
	if !n.Required {
		if len(n.Readings) != 0 {
			return nil, Invalid("无需稳定化时不得提交环境读数", map[string]interface{}{"field": "acclimatization.readings"})
		}
		n.ReleaseDecision = "NOT_REQUIRED"
		n.Digest = stableDigest(n)
		return &n, nil
	}
	if !finite(n.MinimumTemperatureC) || !finite(n.MaximumTemperatureC) || !finite(n.MinimumRelativeHumidity) || !finite(n.MaximumRelativeHumidity) || n.MinimumTemperatureC >= n.MaximumTemperatureC || n.MinimumRelativeHumidity < 0 || n.MaximumRelativeHumidity > 100 || n.MinimumRelativeHumidity >= n.MaximumRelativeHumidity || n.MinimumStableDurationMinutes <= 0 || len(n.Readings) < 2 {
		return nil, Invalid("稳定化范围、时长或读数无效", map[string]interface{}{"field": "acclimatization"})
	}
	for i := range n.Readings {
		r := &n.Readings[i]
		r.MeasuredBy, r.InstrumentID = strings.TrimSpace(r.MeasuredBy), strings.TrimSpace(r.InstrumentID)
		if r.MeasuredAt.IsZero() || r.MeasuredAt.After(assessedAt) || !finite(r.TemperatureC) || !finite(r.RelativeHumidity) || r.RelativeHumidity < 0 || r.RelativeHumidity > 100 || r.MeasuredBy == "" || r.InstrumentID == "" {
			return nil, Invalid("稳定化读数无效", map[string]interface{}{"reading_index": i})
		}
		r.MeasuredAt = r.MeasuredAt.UTC()
		if i > 0 && r.MeasuredAt.Before(n.Readings[i-1].MeasuredAt) {
			return nil, Invalid("稳定化读数时间倒序", map[string]interface{}{"reading_index": i})
		}
		if r.TemperatureC < n.MinimumTemperatureC || r.TemperatureC > n.MaximumTemperatureC || r.RelativeHumidity < n.MinimumRelativeHumidity || r.RelativeHumidity > n.MaximumRelativeHumidity {
			n.AbnormalReadings = append(n.AbnormalReadings, *r)
		}
	}
	windowStart := len(n.Readings)
	for i := len(n.Readings) - 1; i >= 0; i-- {
		r := n.Readings[i]
		inside := r.TemperatureC >= n.MinimumTemperatureC && r.TemperatureC <= n.MaximumTemperatureC && r.RelativeHumidity >= n.MinimumRelativeHumidity && r.RelativeHumidity <= n.MaximumRelativeHumidity
		if !inside {
			break
		}
		windowStart = i
	}
	if windowStart == len(n.Readings) {
		n.ReleaseReasons = append(n.ReleaseReasons, "末次读数仍超出声明范围")
	} else if n.Readings[len(n.Readings)-1].MeasuredAt.Sub(n.Readings[windowStart].MeasuredAt) < time.Duration(n.MinimumStableDurationMinutes)*time.Minute {
		n.ReleaseReasons = append(n.ReleaseReasons, "合格末段未覆盖最短稳定时长")
	}
	if len(n.ReleaseReasons) > 0 {
		return nil, Invalid("播放前环境稳定化未达到放行条件", map[string]interface{}{"release_decision": "NOT_RELEASED", "reasons": n.ReleaseReasons, "abnormal_readings": n.AbnormalReadings})
	}
	n.ReleaseDecision = "RELEASED"
	n.Digest = stableDigest(struct {
		Required bool                     `json:"required"`
		MinT     float64                  `json:"minimum_temperature_c"`
		MaxT     float64                  `json:"maximum_temperature_c"`
		MinRH    float64                  `json:"minimum_relative_humidity"`
		MaxRH    float64                  `json:"maximum_relative_humidity"`
		Minutes  int64                    `json:"minimum_stable_duration_minutes"`
		Readings []AcclimatizationReading `json:"readings"`
	}{n.Required, n.MinimumTemperatureC, n.MaximumTemperatureC, n.MinimumRelativeHumidity, n.MaximumRelativeHumidity, n.MinimumStableDurationMinutes, n.Readings})
	return &n, nil
}

func normalizeCaptureTasks(facets []CarrierFacet, tasks []CaptureTask, skipped []SkippedFacet, planChannels string) ([]CaptureTask, []SkippedFacet, string, int64, error) {
	if tasks == nil && skipped == nil {
		for _, facet := range facets {
			if facet.Playable {
				tasks = append(tasks, CaptureTask{TaskID: "TASK-" + facet.FacetID, FacetID: facet.FacetID, ExecutionOrder: len(tasks) + 1, EstimatedDurationMs: 1, ContentStart: facet.ContentScope, ContentEnd: facet.ContentScope, ChannelMap: planChannels})
			} else {
				skipped = append(skipped, SkippedFacet{FacetID: facet.FacetID, Reason: "登记时标记为不可播放"})
			}
		}
	}
	byFacet := map[string]CarrierFacet{}
	for _, f := range facets {
		byFacet[f.FacetID] = f
	}
	covered, taskIDs, orders := map[string]bool{}, map[string]bool{}, map[int]bool{}
	total := int64(0)
	for i := range tasks {
		t := &tasks[i]
		t.TaskID, t.FacetID = strings.ToUpper(strings.TrimSpace(t.TaskID)), strings.ToUpper(strings.TrimSpace(t.FacetID))
		t.ContentStart, t.ContentEnd, t.ChannelMap = strings.TrimSpace(t.ContentStart), strings.TrimSpace(t.ContentEnd), strings.TrimSpace(t.ChannelMap)
		facet, ok := byFacet[t.FacetID]
		if !ok || !facet.Playable || t.TaskID == "" || taskIDs[t.TaskID] || covered[t.FacetID] || t.ExecutionOrder < 1 || orders[t.ExecutionOrder] || t.EstimatedDurationMs <= 0 || t.ContentStart == "" || t.ContentEnd == "" || !validChannelMap(t.ChannelMap) || !sameChannelSet(t.ChannelMap, planChannels) {
			return nil, nil, "", 0, Invalid("分面采集任务无效", map[string]interface{}{"task_id": t.TaskID, "facet_id": t.FacetID, "item_index": i})
		}
		taskIDs[t.TaskID], covered[t.FacetID], orders[t.ExecutionOrder] = true, true, true
		total += t.EstimatedDurationMs
	}
	for order := 1; order <= len(tasks); order++ {
		if !orders[order] {
			return nil, nil, "", 0, Invalid("采集任务执行顺序必须连续", map[string]interface{}{"missing_execution_order": order})
		}
	}
	for i := range skipped {
		s := &skipped[i]
		s.FacetID, s.Reason = strings.ToUpper(strings.TrimSpace(s.FacetID)), strings.TrimSpace(s.Reason)
		facet, ok := byFacet[s.FacetID]
		if !ok || facet.Playable || covered[s.FacetID] || s.Reason == "" {
			return nil, nil, "", 0, Invalid("不采集分面记录无效", map[string]interface{}{"facet_id": s.FacetID, "item_index": i})
		}
		covered[s.FacetID] = true
	}
	missing := []string{}
	for _, f := range facets {
		if !covered[f.FacetID] {
			missing = append(missing, f.FacetID)
		}
	}
	if len(missing) > 0 || len(covered) != len(facets) {
		return nil, nil, "", 0, Invalid("采集任务未准确覆盖全部分面", map[string]interface{}{"missing_facets": missing})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ExecutionOrder < tasks[j].ExecutionOrder })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].FacetID < skipped[j].FacetID })
	digest := stableDigest(struct {
		Tasks   []CaptureTask  `json:"capture_tasks"`
		Skipped []SkippedFacet `json:"skipped_facets"`
	}{tasks, skipped})
	return tasks, skipped, digest, total, nil
}

func sameChannelSet(a, b string) bool {
	aa, bb := channelNames(a), channelNames(b)
	if len(aa) != len(bb) {
		return false
	}
	ma := map[string]bool{}
	for _, v := range aa {
		ma[v] = true
	}
	for _, v := range bb {
		if !ma[v] {
			return false
		}
	}
	return true
}

func normalizeFixity(g *CaptureGeneration) error {
	if g.FixityChunks == nil {
		if strings.TrimSpace(g.FixityAlgorithm) != "" || g.FixityChunkSizeBytes != 0 {
			return Invalid("fixity_algorithm、fixity_chunk_size_bytes 和 fixity_chunks 必须同时提交", nil)
		}
		return nil
	}
	g.FixityAlgorithm = strings.ToLower(strings.TrimSpace(g.FixityAlgorithm))
	if g.FixityAlgorithm != "sha256" || g.FixityChunkSizeBytes <= 0 || len(g.FixityChunks) == 0 || len(g.FixityChunks) > 100000 {
		return Invalid("分块固化参数无效", map[string]interface{}{"field": "fixity_chunks"})
	}
	total := int64(0)
	h := sha256.New()
	for i := range g.FixityChunks {
		chunk := &g.FixityChunks[i]
		chunk.Digest = strings.ToLower(strings.TrimSpace(chunk.Digest))
		if chunk.Index != i || chunk.SizeBytes <= 0 || !validDigest(chunk.Digest) || (i < len(g.FixityChunks)-1 && chunk.SizeBytes != g.FixityChunkSizeBytes) || (i == len(g.FixityChunks)-1 && chunk.SizeBytes > g.FixityChunkSizeBytes) {
			return Invalid("分块索引、大小或摘要无效", map[string]interface{}{"chunk_index": i})
		}
		decoded, _ := hex.DecodeString(chunk.Digest)
		_, _ = h.Write(decoded)
		total += chunk.SizeBytes
	}
	if total != g.AssetSizeBytes {
		return Invalid("分块字节总数与源文件大小不一致", map[string]interface{}{"chunk_size_total": total, "asset_size_bytes": g.AssetSizeBytes})
	}
	g.FixityDigest = hex.EncodeToString(h.Sum(nil))
	g.FixityCombinationRule = "sha256(concat(binary_chunk_digests))"
	if !strings.EqualFold(g.FixityDigest, g.AssetDigest) {
		return Invalid("分块组合摘要与 asset_digest 不一致", map[string]interface{}{"calculated_asset_digest": g.FixityDigest})
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type qualityThresholds struct {
	DCAbs, LoudnessMin, LoudnessMax, NoiseFloorMax, SilenceMax float64
	Version                                                    string
}

func evaluateChannelMetrics(carrier string, channels []string, metrics []ChannelMetric, profile *MeasurementProfile) ([]ChannelMetric, []MetricResult, []string, string, error) {
	if metrics == nil && profile == nil {
		return nil, nil, nil, "", nil
	}
	if profile == nil {
		return nil, nil, nil, "", Invalid("量化质量指标必须提交 measurement_profile", nil)
	}
	profile.Tool, profile.ToolVersion, profile.ParametersDigest = strings.TrimSpace(profile.Tool), strings.TrimSpace(profile.ToolVersion), strings.ToLower(strings.TrimSpace(profile.ParametersDigest))
	if profile.Tool == "" || profile.ToolVersion == "" || !validDigest(profile.ParametersDigest) {
		return nil, nil, nil, "", Invalid("measurement_profile 无效", nil)
	}
	t := qualityThresholds{0.02, -32, -12, -40, 0.25, "audio-qc-2026.1"}
	if strings.Contains(strings.ToLower(carrier), "tape") || strings.Contains(carrier, "磁带") {
		t.NoiseFloorMax, t.SilenceMax, t.Version = -35, 0.35, "audio-qc-tape-2026.1"
	}
	want, seen := map[string]bool{}, map[string]bool{}
	for _, c := range channels {
		want[c] = true
	}
	normalized := append([]ChannelMetric(nil), metrics...)
	results := []MetricResult{}
	failures := map[string]bool{}
	for i := range normalized {
		m := &normalized[i]
		m.Channel = strings.ToUpper(strings.TrimSpace(m.Channel))
		if !want[m.Channel] || seen[m.Channel] || !finite(m.DCOffset) || !finite(m.IntegratedLoudnessLUFS) || !finite(m.NoiseFloorDBFS) || !finite(m.SilenceRatio) || m.SilenceRatio < 0 || m.SilenceRatio > 1 {
			return nil, nil, nil, "", Invalid("声道量化指标无效", map[string]interface{}{"item_index": i, "channel": m.Channel})
		}
		seen[m.Channel] = true
		add := func(metric string, value float64, min, max *float64, passed bool) {
			results = append(results, MetricResult{Channel: m.Channel, Metric: metric, Value: value, Minimum: min, Maximum: max, Passed: passed})
			if !passed {
				failures[metric] = true
			}
		}
		minOff, maxOff := -t.DCAbs, t.DCAbs
		add("dc_offset", m.DCOffset, &minOff, &maxOff, math.Abs(m.DCOffset) <= t.DCAbs)
		minL, maxL := t.LoudnessMin, t.LoudnessMax
		add("loudness", m.IntegratedLoudnessLUFS, &minL, &maxL, m.IntegratedLoudnessLUFS >= minL && m.IntegratedLoudnessLUFS <= maxL)
		maxNoise := t.NoiseFloorMax
		add("noise_floor", m.NoiseFloorDBFS, nil, &maxNoise, m.NoiseFloorDBFS <= maxNoise)
		maxSilence := t.SilenceMax
		add("silence_ratio", m.SilenceRatio, nil, &maxSilence, m.SilenceRatio <= maxSilence)
	}
	missing := []string{}
	for _, c := range channels {
		if !seen[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 || len(seen) != len(want) {
		return nil, nil, nil, "", Invalid("量化指标未准确覆盖全部计划声道", map[string]interface{}{"missing_channels": missing})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Channel < normalized[j].Channel })
	categories := []string{}
	for _, v := range []string{"dc_offset", "loudness", "noise_floor", "silence_ratio"} {
		if failures[v] {
			categories = append(categories, v)
		}
	}
	return normalized, results, categories, t.Version, nil
}

func (c *DigitizationCase) countersignReasons(generation int) []string {
	reasons := []string{}
	if c.Assessment != nil && strings.EqualFold(c.Assessment.PlaybackRisk, "high") {
		reasons = append(reasons, "high_playback_risk")
	}
	for _, cap := range c.Captures {
		if cap.Generation == generation {
			for _, event := range cap.OperationEvents {
				if event.Type == "splice_repair" {
					reasons = append(reasons, "splice_repair")
				}
			}
		}
	}
	if generation >= 3 {
		reasons = append(reasons, "multiple_recaptures")
	}
	return reasons
}

func (c *DigitizationCase) applyCountersign(q QualityDecision) error {
	var primary *QualityDecision
	for i := len(c.Quality) - 1; i >= 0; i-- {
		if c.Quality[i].QualityRevision == q.CountersignForRevision && c.Quality[i].Generation == c.CurrentCaptureGeneration {
			primary = &c.Quality[i]
			break
		}
	}
	if primary == nil || !primary.RequiresCountersign || primary.CountersignStatus != "PENDING" {
		return Conflict("会签引用的质量材料不是当前待会签 revision", map[string]interface{}{"countersign_for_revision": q.CountersignForRevision})
	}
	reviewer, notes, decision := strings.TrimSpace(q.Reviewer), strings.TrimSpace(q.ListeningNotes), strings.ToUpper(strings.TrimSpace(q.Decision))
	digest := strings.ToLower(strings.TrimSpace(q.ConfirmedEvidenceDigest))
	if reviewer == "" || notes == "" || (decision != "PASS" && decision != "FAIL") || digest != primary.QualityEvidenceDigest {
		return Invalid("独立会签材料无效", map[string]interface{}{"expected_evidence_digest": primary.QualityEvidenceDigest})
	}
	prohibited := map[string]bool{strings.ToLower(primary.Reviewer): true}
	if c.Plan != nil {
		prohibited[strings.ToLower(c.Plan.Operator)] = true
	}
	for _, a := range c.Recaptures {
		for _, r := range a.Remediations {
			prohibited[strings.ToLower(r.Owner)] = true
		}
	}
	if prohibited[strings.ToLower(reviewer)] {
		return Conflict("会签复核员不满足人员分离要求", map[string]interface{}{"reviewer": reviewer})
	}
	agreement := decision == primary.Decision
	disagreements := []string{}
	if !agreement {
		disagreements = append(disagreements, "decision")
	}
	primary.Countersigns = append(primary.Countersigns, QualityCountersign{CountersignForRevision: q.CountersignForRevision, Reviewer: reviewer, Decision: decision, ListeningNotes: notes, ConfirmedEvidenceDigest: digest, ReviewedAt: time.Now().UTC(), Agreement: agreement, Disagreements: disagreements, CountersignRevision: c.Revision + 1})
	if agreement {
		primary.CountersignStatus = "CONFIRMED"
		if decision == "PASS" {
			c.State = StateQCPassed
		} else {
			c.State = StateRecapture
		}
	} else {
		primary.CountersignStatus = "DISAGREED"
		c.State = StateCaptured
	}
	c.Revision++
	return nil
}

func (c *DigitizationCase) activeAuthorization(generation int) (*RecaptureAction, int) {
	for i := len(c.Recaptures) - 1; i >= 0; i-- {
		a := &c.Recaptures[i]
		if a.Generation != generation {
			continue
		}
		if a.Action == "revoke" {
			return nil, -1
		}
		if a.Action == "authorize" || a.Action == "renew" {
			return a, i
		}
	}
	return nil, -1
}

func (c *DigitizationCase) RevokeRecapture(r RecaptureAction) error {
	if c.State != StateReady && c.State != StateRecapture {
		return ErrState
	}
	current, _ := c.activeAuthorization(c.CurrentCaptureGeneration + 1)
	if current == nil || current.ConsumedAt != nil || current.Status != "ACTIVE" {
		return Conflict("当前重采授权不可撤销", nil)
	}
	by := strings.TrimSpace(r.RevokedBy)
	if by == "" {
		by = strings.TrimSpace(r.AuthorizedBy)
	}
	reason := strings.TrimSpace(r.RevocationReason)
	if reason == "" {
		reason = strings.TrimSpace(r.Reason)
	}
	if by == "" || reason == "" {
		return Invalid("撤销负责人和原因必填", nil)
	}
	if strings.EqualFold(by, current.AuthorizedBy) {
		return Invalid("撤销负责人不得与原授权人相同", nil)
	}
	if r.AuthorizationVersion != 0 && r.AuthorizationVersion != current.AuthorizationVersion {
		return Conflict("重采授权版本不是当前版本", map[string]interface{}{"current_authorization_version": current.AuthorizationVersion})
	}
	now := time.Now().UTC()
	r = RecaptureAction{Action: "revoke", AuthorizationVersion: current.AuthorizationVersion, Status: "REVOKED", Generation: current.Generation, FailedQualityGeneration: current.FailedQualityGeneration, At: now, RevokedBy: by, RevokedAt: &now, RevocationReason: reason}
	c.Recaptures = append(c.Recaptures, r)
	c.State = StateRecapture
	c.Revision++
	return nil
}

func (c *DigitizationCase) RenewRecapture(r RecaptureAction) error {
	if c.State != StateReady && c.State != StateRecapture {
		return ErrState
	}
	var current *RecaptureAction
	for i := len(c.Recaptures) - 1; i >= 0; i-- {
		candidate := &c.Recaptures[i]
		if candidate.Generation == c.CurrentCaptureGeneration+1 && (candidate.Action == "authorize" || candidate.Action == "renew") {
			current = candidate
			break
		}
	}
	if current == nil {
		return Conflict("缺少可续期的重采授权", nil)
	}
	if r.AuthorizationVersion != 0 && r.AuthorizationVersion != current.AuthorizationVersion {
		return Conflict("续期引用的授权版本不是当前版本", map[string]interface{}{"current_authorization_version": current.AuthorizationVersion})
	}
	latest := c.Recaptures[len(c.Recaptures)-1]
	now := time.Now().UTC()
	revoked := latest.Action == "revoke" && latest.AuthorizationVersion == current.AuthorizationVersion
	if !revoked && current.ExpiresAt.After(now) {
		return Conflict("只有已到期或已撤销授权可以续期", nil)
	}
	r.Action = "renew"
	r.AuthorizationVersion = current.AuthorizationVersion + 1
	r.RenewsVersion = current.AuthorizationVersion
	r.Generation = current.Generation
	r.RequestedFailedGeneration = current.FailedQualityGeneration
	// 复用授权门禁，但保持目标 generation 不变。
	return c.authorizeRecaptureVersion(r, true)
}

func (c *DigitizationCase) BuildGenerationEvidenceIndex() ([]GenerationEvidence, error) {
	index := make([]GenerationEvidence, 0, len(c.Captures))
	for _, cap := range c.Captures {
		var plan *CapturePlan
		for i := range c.PlanHistory {
			if c.PlanHistory[i].PlanRevision == cap.PlanRevision {
				plan = &c.PlanHistory[i]
				break
			}
		}
		if plan == nil {
			return nil, Conflict("保存包关系引用不存在的方案版本", map[string]interface{}{"generation": cap.Generation, "field": "plan_revision"})
		}
		var task *CaptureTask
		for i := range plan.CaptureTasks {
			if plan.CaptureTasks[i].TaskID == cap.CaptureTaskID {
				task = &plan.CaptureTasks[i]
				break
			}
		}
		if task == nil {
			return nil, Conflict("保存包关系引用不存在的采集任务", map[string]interface{}{"generation": cap.Generation, "field": "capture_task_id"})
		}
		var q *QualityDecision
		for i := len(c.Quality) - 1; i >= 0; i-- {
			candidate := &c.Quality[i]
			if candidate.Generation == cap.Generation && (!candidate.RequiresCountersign || candidate.CountersignStatus == "CONFIRMED" || candidate.CountersignStatus == "ADJUDICATED") {
				q = candidate
				break
			}
		}
		if q == nil {
			return nil, Conflict("保存包关系缺少质量决定", map[string]interface{}{"generation": cap.Generation, "field": "quality_revision"})
		}
		rel := GenerationEvidence{Generation: cap.Generation, PlanRevision: cap.PlanRevision, CaptureTaskID: cap.CaptureTaskID, TaskOrder: task.ExecutionOrder, AssetDigest: cap.AssetDigest, QualityRevision: q.QualityRevision, QualityDecision: effectiveQualityDecision(*q)}
		if cap.Generation > 1 {
			var prev *QualityDecision
			for i := len(c.Quality) - 1; i >= 0; i-- {
				if c.Quality[i].Generation == cap.Generation-1 && effectiveQualityDecision(c.Quality[i]) == "FAIL" {
					prev = &c.Quality[i]
					break
				}
			}
			auth, _ := c.activeAuthorization(cap.Generation)
			if prev == nil || auth == nil || auth.ConsumedAt == nil || auth.AuthorizationVersion != cap.RecaptureAuthorizationVersion {
				return nil, Conflict("保存包重采关系不完整", map[string]interface{}{"generation": cap.Generation, "field": "recapture_authorization_version"})
			}
			rel.FailedQualityRevision = prev.QualityRevision
			rel.RecaptureAuthorizationVersion = auth.AuthorizationVersion
		}
		index = append(index, rel)
	}
	sort.Slice(index, func(i, j int) bool {
		if index[i].Generation != index[j].Generation {
			return index[i].Generation < index[j].Generation
		}
		return index[i].TaskOrder < index[j].TaskOrder
	})
	return index, nil
}
