package application

import (
	"archiveflow/internal/domain"
	"encoding/json"
	"errors"
	"strings"
)

type CapturePreflightItem struct {
	Index                   int                    `json:"index"`
	CaptureTaskID           string                 `json:"capture_task_id,omitempty"`
	Valid                   bool                   `json:"valid"`
	Generation              int                    `json:"generation"`
	TechnicalEvidenceDigest string                 `json:"technical_evidence_digest,omitempty"`
	Errors                  map[string]string      `json:"errors,omitempty"`
	Details                 map[string]interface{} `json:"details,omitempty"`
}

type CapturePreflightReport struct {
	RequestID               string                 `json:"request_id"`
	CaseID                  string                 `json:"case_id"`
	CurrentRevision         int64                  `json:"current_revision"`
	Revision                int64                  `json:"revision"`
	PlanRevision            int64                  `json:"plan_revision"`
	CurrentPlanRevision     int64                  `json:"current_plan_revision"`
	PlanFingerprint         string                 `json:"plan_fingerprint"`
	NextGeneration          int                    `json:"next_generation"`
	Valid                   bool                   `json:"valid"`
	Results                 []CapturePreflightItem `json:"results"`
	TechnicalEvidenceDigest string                 `json:"technical_evidence_digest"`
	ReportDigest            string                 `json:"report_digest"`
	AuditHead               string                 `json:"audit_head"`
	NoAuditEvent            bool                   `json:"no_audit_event"`
}

// CapturePreflight 在个案快照克隆上执行正式采集的全部领域门禁，不写入个案或审计。
func (a *App) CapturePreflight(requestID, id string, rev int64, items []domain.CaptureGeneration) (CapturePreflightReport, error) {
	if strings.TrimSpace(requestID) == "" || len(items) == 0 || len(items) > 100 {
		return CapturePreflightReport{}, domain.ErrInvalid
	}
	payload := struct {
		Revision int64                      `json:"expected_revision"`
		Items    []domain.CaptureGeneration `json:"items"`
	}{rev, items}
	key := "CAPTURE_PREFLIGHT:" + id + ":" + requestID
	memoryKey := key + ":" + mustDigest(payload)
	if report, ok := a.cachedCapturePreflight(memoryKey); ok {
		return report, nil
	}
	if b, ok := a.Store.GetIdempotency(key); ok {
		var saved struct {
			Digest string                 `json:"digest"`
			Report CapturePreflightReport `json:"report"`
		}
		if json.Unmarshal(b, &saved) != nil {
			return CapturePreflightReport{}, domain.ErrIntegrity
		}
		d, _ := digest(payload)
		if saved.Digest != d {
			return CapturePreflightReport{}, domain.ErrConflict
		}
		a.rememberCapturePreflight(memoryKey, saved.Report)
		return saved.Report, nil
	}
	unlock := a.lock(id)
	defer unlock()
	if b, ok := a.Store.GetIdempotency(key); ok {
		var saved struct {
			Digest string                 `json:"digest"`
			Report CapturePreflightReport `json:"report"`
		}
		if json.Unmarshal(b, &saved) != nil {
			return CapturePreflightReport{}, domain.ErrIntegrity
		}
		d, _ := digest(payload)
		if saved.Digest != d {
			return CapturePreflightReport{}, domain.ErrConflict
		}
		a.rememberCapturePreflight(memoryKey, saved.Report)
		return saved.Report, nil
	}
	c, err := a.Store.Get(id)
	if err != nil {
		return CapturePreflightReport{}, err
	}
	if c.Revision != rev {
		return CapturePreflightReport{}, domain.ErrConflict
	}
	report := CapturePreflightReport{RequestID: requestID, CaseID: id, CurrentRevision: c.Revision, Revision: c.Revision, NextGeneration: c.CurrentCaptureGeneration + 1, Results: make([]CapturePreflightItem, len(items)), AuditHead: a.Audit.Head(id), NoAuditEvent: true}
	if c.Plan != nil {
		report.PlanRevision, report.CurrentPlanRevision, report.PlanFingerprint = c.Plan.PlanRevision, c.Plan.PlanRevision, c.Plan.Fingerprint
	}
	for i, item := range items {
		r := CapturePreflightItem{Index: i, CaptureTaskID: strings.ToUpper(strings.TrimSpace(item.CaptureTaskID)), Generation: report.NextGeneration, Errors: map[string]string{}}
		clone := cloneForPreflight(c)
		err := clone.AddCapture(item)
		if err != nil {
			r.Valid = false
			r.Errors[preflightField(err)] = err.Error()
			if de, ok := err.(*domain.DetailError); ok {
				r.Details = de.Details
			}
		} else {
			r.Valid = true
			last := clone.Captures[len(clone.Captures)-1]
			r.CaptureTaskID, r.Generation, r.TechnicalEvidenceDigest = last.CaptureTaskID, last.Generation, last.TechnicalEvidenceDigest
		}
		report.Results[i] = r
	}
	// 成组门禁负责任务覆盖、提交顺序和组内摘要唯一性；错误只报告，不保存任何代次。
	group := cloneForPreflight(c)
	if err := group.AddCaptureGroup(items); err != nil {
		if len(report.Results) == 1 && report.Results[0].Valid {
			report.Results[0].Valid = false
			report.Results[0].Errors[preflightField(err)] = err.Error()
		} else if de, ok := err.(*domain.DetailError); ok {
			idx := detailIndex(de.Details)
			if idx >= 0 && idx < len(report.Results) {
				report.Results[idx].Valid = false
				report.Results[idx].Errors[preflightField(err)] = err.Error()
				report.Results[idx].Details = de.Details
			} else {
				for i := range report.Results {
					report.Results[i].Errors[preflightField(err)] = err.Error()
					report.Results[i].Valid = false
				}
			}
		} else {
			for i := range report.Results {
				report.Results[i].Errors[preflightField(err)] = err.Error()
				report.Results[i].Valid = false
			}
		}
	}
	seen := map[string]int{}
	for i, item := range items {
		d := strings.ToLower(strings.TrimSpace(item.AssetDigest))
		if d == "" {
			continue
		}
		if prev, ok := seen[d]; ok {
			report.Results[i].Valid = false
			report.Results[i].Errors["asset_digest"] = "成组采集资产摘要重复"
			report.Results[prev].Valid = false
			report.Results[prev].Errors["asset_digest"] = "成组采集资产摘要重复"
		} else {
			seen[d] = i
		}
		if field, conflict := a.Store.CaptureEvidenceConflict("", "", item.CalibratedAt, item.CalibrationValidUntil, d); conflict {
			report.Results[i].Valid = false
			report.Results[i].Errors[field] = "采集证据与历史记录冲突"
		}
	}
	valid := true
	digests := make([]string, 0, len(report.Results))
	for _, r := range report.Results {
		if !r.Valid {
			valid = false
		}
		if r.TechnicalEvidenceDigest != "" {
			digests = append(digests, r.TechnicalEvidenceDigest)
		}
	}
	report.Valid = valid
	report.TechnicalEvidenceDigest, _ = digest(digests)
	without := report
	without.ReportDigest = ""
	report.ReportDigest, _ = digest(without)
	b, _ := json.Marshal(struct {
		Digest string                 `json:"digest"`
		Report CapturePreflightReport `json:"report"`
	}{mustDigest(payload), report})
	if err := a.Store.PutIdempotency(key, b); err != nil {
		return CapturePreflightReport{}, err
	}
	a.rememberCapturePreflight(memoryKey, report)
	return report, nil
}

func (a *App) cachedCapturePreflight(key string) (CapturePreflightReport, bool) {
	a.preflightMu.RLock()
	defer a.preflightMu.RUnlock()
	report, ok := a.preflightReports[key]
	return report, ok
}

func (a *App) rememberCapturePreflight(key string, report CapturePreflightReport) {
	a.preflightMu.Lock()
	defer a.preflightMu.Unlock()
	a.preflightReports[key] = report
}

func cloneForPreflight(c *domain.DigitizationCase) *domain.DigitizationCase {
	b, _ := json.Marshal(c)
	var out domain.DigitizationCase
	_ = json.Unmarshal(b, &out)
	return &out
}
func mustDigest(v interface{}) string { d, _ := digest(v); return d }
func preflightField(err error) string {
	if de, ok := err.(*domain.DetailError); ok {
		if de.Details != nil {
			if f, ok := de.Details["field"].(string); ok && f != "" {
				return f
			}
			if _, ok := de.Details["missing_tasks"]; ok {
				return "task_coverage"
			}
			if _, ok := de.Details["missing_facets"]; ok {
				return "task_coverage"
			}
			if _, ok := de.Details["chunk_index"]; ok {
				return "fixity_chunks"
			}
			if _, ok := de.Details["item_index"]; ok {
				return "item"
			}
		}
		return "evidence"
	}
	if errors.Is(err, domain.ErrConflict) {
		return "conflict"
	}
	if errors.Is(err, domain.ErrInvalid) {
		return "evidence"
	}
	if errors.Is(err, domain.ErrState) {
		return "state"
	}
	return "state"
}
func detailIndex(details map[string]interface{}) int {
	if details == nil {
		return -1
	}
	if v, ok := details["item_index"].(int); ok {
		return v
	}
	if v, ok := details["item_index"].(float64); ok {
		return int(v)
	}
	return -1
}
