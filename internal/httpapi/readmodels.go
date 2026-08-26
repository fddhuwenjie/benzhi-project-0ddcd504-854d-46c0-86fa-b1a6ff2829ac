package httpapi

import (
	"archiveflow/internal/domain"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func (s *Server) handleCustodyChain(w http.ResponseWriter, r *http.Request, id string) {
	from, err := queryTime(r, "from_time")
	if err != nil {
		out(w, map[string]string{"error": "from_time 无效"}, http.StatusBadRequest)
		return
	}
	to, err := queryTime(r, "to_time")
	if err != nil {
		out(w, map[string]string{"error": "to_time 无效"}, http.StatusBadRequest)
		return
	}
	if from != nil && to != nil && to.Before(*from) {
		out(w, map[string]string{"error": "to_time 不能早于 from_time"}, http.StatusBadRequest)
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			out(w, map[string]interface{}{"error": "limit 无效", "maximum_limit": 100}, http.StatusBadRequest)
			return
		}
	}
	includeEvents := true
	if value := strings.TrimSpace(r.URL.Query().Get("include_events")); value != "" {
		includeEvents, err = strconv.ParseBool(value)
		if err != nil {
			out(w, map[string]string{"error": "include_events 无效"}, http.StatusBadRequest)
			return
		}
	}
	result, err := s.App.CustodyChain(id, from, to, limit, includeEvents)
	if err != nil {
		parseErr(w, err)
		return
	}
	// Keep a stable empty array for callers requesting a metadata-only response.
	if result.Events == nil {
		result.Events = []domain.CustodyEvent{}
	}
	out(w, result, http.StatusOK)
}

func (s *Server) handleCaptureLedger(w http.ResponseWriter, r *http.Request, id string) {
	c, err := s.captureLedgerCase(id)
	if err != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	}
	gen := 0
	if v := r.URL.Query().Get("generation"); v != "" {
		gen, err = strconv.Atoi(v)
		if err != nil || gen < 1 {
			out(w, map[string]string{"error": "generation 无效"}, 400)
			return
		}
	}
	task := strings.TrimSpace(r.URL.Query().Get("capture_task_id"))
	planRev := int64(0)
	if v := r.URL.Query().Get("plan_revision"); v != "" {
		planRev, err = strconv.ParseInt(v, 10, 64)
		if err != nil || planRev < 1 {
			out(w, map[string]string{"error": "plan_revision 无效"}, 400)
			return
		}
	}
	captures := []domain.CaptureGeneration{}
	foundGen, foundTask := false, false
	ledger := []map[string]interface{}{}
	for _, g := range c.Captures {
		if gen > 0 && g.Generation != gen {
			continue
		}
		if task != "" && g.CaptureTaskID != task {
			continue
		}
		if planRev > 0 && g.PlanRevision != planRev {
			continue
		}
		captures = append(captures, g)
		integrity := "ok"
		fields := []string{}
		if g.TechnicalEvidenceDigest == "" || g.TechnicalEvidenceDigest != domain.CaptureTechnicalDigest(g) {
			integrity = "integrity_error"
			fields = append(fields, "technical_evidence_digest")
		}
		if g.FixityDigest != "" && !strings.EqualFold(g.FixityDigest, g.AssetDigest) {
			integrity = "integrity_error"
			fields = append(fields, "fixity_digest")
		}
		ledger = append(ledger, map[string]interface{}{"generation": g.Generation, "capture_task_id": g.CaptureTaskID, "plan_revision": g.PlanRevision, "asset_digest": g.AssetDigest, "technical_evidence_digest": g.TechnicalEvidenceDigest, "fixity_digest": g.FixityDigest, "integrity_status": integrity, "integrity_fields": fields, "capture": g})
		if gen > 0 {
			foundGen = true
		}
		if task != "" {
			foundTask = true
		}
	}
	if gen > 0 && !foundGen {
		out(w, map[string]string{"error": "未找到该采集代次"}, 404)
		return
	}
	if task != "" && !foundTask {
		known := false
		if c.Plan != nil {
			for _, t := range c.Plan.CaptureTasks {
				if t.TaskID == task {
					known = true
				}
			}
		}
		if !known {
			out(w, map[string]string{"error": "未找到该采集任务"}, 404)
			return
		}
	}
	sort.SliceStable(captures, func(i, j int) bool {
		if captures[i].Generation != captures[j].Generation {
			return captures[i].Generation < captures[j].Generation
		}
		return captures[i].CaptureTaskID < captures[j].CaptureTaskID
	})
	matrix := []map[string]interface{}{}
	if c.Plan != nil {
		for _, t := range c.Plan.CaptureTasks {
			status := "pending"
			for _, g := range c.Captures {
				if g.Generation == c.CurrentCaptureGeneration && g.CaptureTaskID == t.TaskID {
					status = "completed"
				}
			}
			matrix = append(matrix, map[string]interface{}{"capture_task_id": t.TaskID, "execution_order": t.ExecutionOrder, "status": status})
		}
		for _, sk := range c.Plan.SkippedFacets {
			matrix = append(matrix, map[string]interface{}{"facet_id": sk.FacetID, "status": "skipped", "reason": sk.Reason})
		}
	}
	out(w, map[string]interface{}{"case_id": id, "generation": c.CurrentCaptureGeneration, "captures": captures, "items": captures, "ledger": ledger, "count": len(captures), "task_matrix": matrix, "audit_head": s.App.Audit.Head(id), "audit_revision": c.Revision}, 200)
}

func (s *Server) captureLedgerCase(id string) (*domain.DigitizationCase, error) {
	s.captureLedgerMu.Lock()
	defer s.captureLedgerMu.Unlock()
	if cached, ok := s.captureLedgerCases[id]; ok {
		return cached, nil
	}
	c, err := s.App.Store.Get(id)
	if err != nil {
		return nil, err
	}
	s.captureLedgerCases[id] = c
	return c, nil
}

func (s *Server) handleQualitySummary(w http.ResponseWriter, r *http.Request, id string) {
	c, err := s.App.Store.Get(id)
	if err != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	}
	genFilter := 0
	if v := r.URL.Query().Get("generation"); v != "" {
		genFilter, err = strconv.Atoi(v)
		if err != nil || genFilter < 1 {
			out(w, map[string]string{"error": "generation 无效"}, 400)
			return
		}
	}
	cat := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("failure_category")))
	min := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("minimum_severity")))
	if min != "" && min != "MINOR" && min != "MAJOR" && min != "CRITICAL" {
		out(w, map[string]string{"error": "minimum_severity 无效"}, 400)
		return
	}
	series := []map[string]interface{}{}
	pass, fail := 0, 0
	persistent := map[string]bool{}
	resolved := map[string]bool{}
	newCats := map[string]bool{}
	pending := false
	for _, q := range c.Quality {
		if genFilter > 0 && q.Generation != genFilter {
			continue
		}
		if cat != "" && !contains(q.FailureCategories, cat) {
			continue
		}
		if min != "" && !qualitySeverityAtLeast(q, min) {
			continue
		}
		if q.Decision == "PASS" {
			pass++
		} else if q.Decision == "FAIL" {
			fail++
		}
		if q.CountersignStatus == "PENDING" || q.Generation != c.CurrentCaptureGeneration || q.QualityRevision <= 0 || q.QualityEvidenceDigest == "" {
			pending = true
		}
		for _, v := range q.PersistentCategories {
			persistent[v] = true
		}
		for _, v := range q.ResolvedCategories {
			resolved[v] = true
		}
		for _, v := range q.NewCategories {
			newCats[v] = true
		}
		series = append(series, map[string]interface{}{"generation": q.Generation, "decision": q.Decision, "failure_categories": q.FailureCategories, "failure_summary": q.FailureSummary, "quality_revision": q.QualityRevision, "listening_coverage": q.ListeningCoverage, "countersign_status": q.CountersignStatus, "adjudication": q.Adjudication, "resolved_categories": q.ResolvedCategories, "persistent_categories": q.PersistentCategories, "new_categories": q.NewCategories, "remediation_effect_digest": q.RemediationEffectDigest, "channel_metrics": q.ChannelMetrics})
	}
	if genFilter > 0 && len(series) == 0 {
		out(w, map[string]string{"error": "未找到该质量代次"}, 404)
		return
	}
	rate := float64(0)
	if pass+fail > 0 {
		rate = float64(pass) / float64(pass+fail)
	}
	status := "ready"
	if pending {
		status = "pending_review"
	}
	out(w, map[string]interface{}{"case_id": id, "generations": series, "items": series, "pass_rate": rate, "passed_generations": pass, "failed_generations": fail, "failure_generations": fail, "resolved_categories": keys(resolved), "persistent_categories": keys(persistent), "new_categories": keys(newCats), "status": status, "pending_review": pending, "integrity_status": map[bool]string{true: "pending_review", false: "ok"}[pending], "current_generation": c.CurrentCaptureGeneration}, 200)
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
func qualitySeverityAtLeast(q domain.QualityDecision, min string) bool {
	for _, m := range q.DefectMarkers {
		if domain.SeverityAtLeast(m.Severity, min) {
			return true
		}
	}
	return false
}
func keys(m map[string]bool) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
