package httpapi

import (
	"archiveflow/internal/application"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct{ App *application.App }

func New(a *application.App) *Server { return &Server{App: a} }
func (s *Server) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { out(w, map[string]string{"status": "ok"}, 200) })
	m.HandleFunc("/v1/cases", s.cases)
	m.HandleFunc("/v1/cases/", s.caseAction)
	return m
}
func dec(r *http.Request, v interface{}) error {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing interface{}
	if err := d.Decode(&trailing); err != io.EOF {
		return errors.New("请求体只能包含一个 JSON 值")
	}
	return nil
}
func out(w http.ResponseWriter, v interface{}, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func (s *Server) cases(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		state := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("state")))
		if state != "" {
			valid := map[string]bool{}
			for _, v := range []domain.State{domain.StateRegistered, domain.StateAssessed, domain.StateReady, domain.StateCaptured, domain.StateRecapture, domain.StateQCPassed, domain.StateSealed} {
				valid[string(v)] = true
			}
			if !valid[state] {
				out(w, map[string]string{"error": "未知 state"}, 400)
				return
			}
		}
		prefix := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("accession_prefix")))
		if prefix == "" {
			prefix = strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("accession")))
		}
		alternativeIdentifier := strings.ToUpper(strings.Join(strings.Fields(r.URL.Query().Get("alternative_identifier")), " "))
		if alternativeIdentifier == "" {
			alternativeIdentifier = strings.ToUpper(strings.Join(strings.Fields(r.URL.Query().Get("identifier")), " "))
		}
		title := r.URL.Query().Get("title")
		if title == "" {
			title = r.URL.Query().Get("title_keyword")
		}
		failureCategory := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("failure_category")))
		validFailure := map[string]bool{"": true, "completeness": true, "clipping": true, "dropout": true, "channel_mapping": true, "duration_variance": true, "listening_anomaly": true, "dc_offset": true, "loudness": true, "noise_floor": true, "silence_ratio": true}
		if !validFailure[failureCategory] {
			out(w, map[string]string{"error": "failure_category 无效"}, 400)
			return
		}
		minimumSeverity := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("minimum_severity")))
		if minimumSeverity == "" {
			minimumSeverity = strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("min_severity")))
		}
		if minimumSeverity != "" && minimumSeverity != "MINOR" && minimumSeverity != "MAJOR" && minimumSeverity != "CRITICAL" {
			out(w, map[string]string{"error": "minimum_severity 无效"}, 400)
			return
		}
		riskCategory := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("risk_category")))
		playbackRisk := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("playback_risk")))
		treatmentStatus := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("treatment_status")))
		assessmentVersion := 0
		var err error
		if v := strings.TrimSpace(r.URL.Query().Get("assessment_version")); v != "" {
			assessmentVersion, err = strconv.Atoi(v)
			if err != nil || assessmentVersion < 1 {
				out(w, map[string]string{"error": "assessment_version 无效"}, 400)
				return
			}
		}
		if treatmentStatus != "" && treatmentStatus != "pending" && treatmentStatus != "completed" && treatmentStatus != "ready" {
			out(w, map[string]string{"error": "treatment_status 无效"}, 400)
			return
		}
		integrityCheck := false
		if value := strings.TrimSpace(r.URL.Query().Get("integrity_check")); value != "" {
			var parseBoolErr error
			integrityCheck, parseBoolErr = strconv.ParseBool(value)
			if parseBoolErr != nil {
				out(w, map[string]string{"error": "integrity_check 无效"}, 400)
				return
			}
		}
		sealedAfter, err := queryTime(r, "sealed_after")
		if err != nil {
			out(w, map[string]string{"error": "sealed_after 无效"}, 400)
			return
		}
		sealedBefore, err := queryTime(r, "sealed_before")
		if err != nil || (sealedAfter != nil && sealedBefore != nil && sealedAfter.After(*sealedBefore)) {
			out(w, map[string]string{"error": "sealed_before 无效"}, 400)
			return
		}
		offset, limit := 0, 0
		if v := r.URL.Query().Get("offset"); v != "" {
			offset, err = strconv.Atoi(v)
			if err != nil || offset < 0 {
				out(w, map[string]string{"error": "offset 无效"}, 400)
				return
			}
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			limit, err = strconv.Atoi(v)
			if err != nil || limit < 0 || limit > 1000 {
				out(w, map[string]string{"error": "limit 无效"}, 400)
				return
			}
		}
		if integrityCheck && (state != string(domain.StateSealed) || limit < 1 || limit > 100) {
			out(w, map[string]interface{}{"error": "integrity_check 仅支持 state=SEALED 且 limit 必须在 1 到 100 之间", "maximum_limit": 100}, 400)
			return
		}
		filter := store.Filter{State: state, AccessionPrefix: prefix, AlternativeIdentifier: alternativeIdentifier, Title: title, FailureCategory: failureCategory, MinimumSeverity: minimumSeverity, SealedAfter: sealedAfter, SealedBefore: sealedBefore, RiskCategory: riskCategory, PlaybackRisk: playbackRisk, AssessmentVersion: assessmentVersion, TreatmentStatus: treatmentStatus}
		matched, total := s.App.Store.Search(filter)
		filter.Offset, filter.Limit = offset, limit
		items, _ := s.App.Store.Search(filter)
		for _, item := range items {
			if item.Plan != nil {
				item.Plan.RefreshValidity(time.Now().UTC())
				for i := range item.PlanHistory {
					item.PlanHistory[i].RefreshValidity(time.Now().UTC())
				}
			}
			requirement := item.EscalationStatus()
			if requirement.Required {
				item.CurrentEscalationRequirement = &requirement
			}
			if item.FirstAuditAt.IsZero() {
				item.FirstAuditAt = s.App.Audit.FirstAt(item.ID)
			}
		}
		stats := map[string]interface{}{"registered": 0, "assessed": 0, "ready_for_capture": 0, "captured": 0, "recapture_required": 0, "qc_passed": 0, "sealed": 0, "quality_pass": 0, "quality_fail": 0, "recapture_generations": 0, "sealed_manifests": 0, "pending_review_count": 0}
		failureCounts := map[string]int{}
		severityCounts := map[string]int{"MINOR": 0, "MAJOR": 0, "CRITICAL": 0}
		qualityPass, qualityFail := 0, 0
		for _, c := range matched {
			key := strings.ToLower(string(c.State))
			stats[key] = stats[key].(int) + 1
			if c.State == domain.StateCaptured {
				stats["pending_review_count"] = stats["pending_review_count"].(int) + 1
			}
			if c.Manifest != nil {
				stats["sealed_manifests"] = stats["sealed_manifests"].(int) + 1
			}
			stats["recapture_generations"] = stats["recapture_generations"].(int) + len(c.Recaptures)
			for _, q := range c.Quality {
				if q.Decision == "PASS" {
					qualityPass++
				} else if q.Decision == "FAIL" {
					qualityFail++
				}
				for _, category := range q.FailureCategories {
					failureCounts[category]++
				}
				for _, marker := range q.DefectMarkers {
					severityCounts[marker.Severity]++
				}
			}
		}
		stats["quality_pass"], stats["quality_fail"] = qualityPass, qualityFail
		stats["quality_pass_count"] = qualityPass
		stats["quality_fail_count"] = qualityFail
		stats["recapture_count"] = stats["recapture_generations"]
		stats["sealed_count"] = stats["sealed_manifests"]
		stats["failure_categories"] = failureCounts
		stats["defect_severity"] = severityCounts
		treatmentCounts := map[string]int{"pending": 0, "completed": 0, "ready": 0}
		riskCounts := map[string]int{}
		for _, c := range matched {
			if c.Assessment == nil {
				treatmentCounts["pending"]++
				continue
			}
			status := "pending"
			required := []string{}
			if c.Assessment.MoldLevel != "none" {
				required = append(required, "mold")
			}
			if c.Assessment.Breakage {
				required = append(required, "breakage")
			}
			if c.Assessment.Adhesion {
				required = append(required, "adhesion")
			}
			if c.Assessment.Contamination {
				required = append(required, "contamination")
			}
			if c.Assessment.PlaybackRisk == "high" {
				required = append(required, "playback")
			}
			covered := true
			for _, rc := range required {
				if _, ok := c.Assessment.TreatmentCoverage[rc]; !ok {
					covered = false
				}
			}
			if covered {
				status = "completed"
				if c.Assessment.Acclimatization == nil || !c.Assessment.Acclimatization.Required || c.Assessment.Acclimatization.ReleaseDecision == "RELEASED" {
					status = "ready"
				}
			}
			treatmentCounts[status]++
			for _, rc := range c.Assessment.RiskCategories {
				base := rc
				if i := strings.Index(base, ":"); i >= 0 {
					base = base[:i]
				}
				riskCounts[base]++
			}
		}
		stats["treatment_status"] = treatmentCounts
		stats["risk_categories"] = riskCounts
		passRate := float64(0)
		if qualityPass+qualityFail > 0 {
			passRate = float64(qualityPass) / float64(qualityPass+qualityFail)
		}
		stats["quality_pass_rate"] = passRate
		byState := map[string]int{}
		for _, c := range matched {
			byState[string(c.State)]++
		}
		auditHeads := map[string]string{}
		for _, c := range items {
			auditHeads[c.ID] = s.App.Audit.Head(c.ID)
		}
		response := map[string]interface{}{"service": "archiveflow", "cases": items, "items": items, "count": len(items), "total": total, "offset": offset, "limit": limit, "stats": stats, "by_state": byState, "audit_heads": auditHeads}
		if integrityCheck {
			results, integrityStats := s.App.IntegrityChecks(items, matched)
			response["integrity_results"], response["integrity_stats"] = results, integrityStats
		}
		out(w, response, 200)
		return
	}
	if r.Method != "POST" {
		out(w, map[string]string{"error": "不支持此请求方法"}, 405)
		return
	}
	if !jsonContentType(r) {
		out(w, map[string]string{"error": "Content-Type 必须为 application/json"}, 415)
		return
	}
	var raw map[string]json.RawMessage
	if dec(r, &raw) != nil {
		out(w, map[string]string{"error": "请求体无效"}, 400)
		return
	}
	req := requestID(r, raw)
	if req == "" {
		out(w, map[string]string{"error": "request_id 为必填项"}, 400)
		return
	}
	if v, ok := raw["preflight"]; ok {
		var pf bool
		_ = json.Unmarshal(v, &pf)
		if pf {
			s.handlePreflight(w, req, raw)
			return
		}
	}
	if _, batch := raw["items"]; batch {
		s.handleRegistrationBatch(w, req, raw)
		return
	}
	var x struct {
		AccessionCode          string                         `json:"accession_code"`
		Title                  string                         `json:"title"`
		RightsNote             string                         `json:"rights_note"`
		CarrierType            string                         `json:"carrier_type"`
		ContentScope           string                         `json:"content_scope"`
		IntakeReceipt          *domain.IntakeReceipt          `json:"intake_receipt"`
		CarrierFacets          []domain.CarrierFacet          `json:"carrier_facets"`
		AlternativeIdentifiers []domain.AlternativeIdentifier `json:"alternative_identifiers"`
		CustodyEvents          []domain.CustodyEvent          `json:"custody_events"`
	}
	normalizeArrayAliases(raw, "carrier_facets", map[string]string{"label_text": "label", "expected_content_scope": "content_scope"})
	normalizeArrayAliases(raw, "custody_events", custodyAliases())
	if unknown(raw, map[string]bool{"request_id": true, "accession_code": true, "title": true, "rights_note": true, "carrier_type": true, "content_scope": true, "intake_receipt": true, "carrier_facets": true, "alternative_identifiers": true, "custody_events": true}) {
		out(w, map[string]string{"error": "存在未知字段"}, 400)
		return
	}
	if json.Unmarshal(rawBytes(raw), &x) != nil {
		out(w, map[string]string{"error": "请求体无效"}, 400)
		return
	}
	if receiptRaw, exists := raw["intake_receipt"]; exists {
		var receiptFields map[string]json.RawMessage
		if json.Unmarshal(receiptRaw, &receiptFields) != nil || unknown(receiptFields, intakeReceiptFields()) {
			out(w, map[string]string{"error": "intake_receipt 无效"}, 400)
			return
		}
		for _, field := range []string{"transfer_organization", "transferor", "receiver", "received_at", "batch_number", "packaging_condition"} {
			if _, ok := receiptFields[field]; !ok {
				out(w, map[string]string{"error": "intake_receipt." + field + " 为必填项"}, 400)
				return
			}
		}
	}
	if index, field, ok := validateObjectArray(raw, "carrier_facets", map[string]bool{"facet_id": true, "label": true, "physical_order": true, "content_scope": true, "playable": true}); !ok {
		out(w, map[string]interface{}{"error": "carrier_facets 无效", "item_index": index, "field": field}, 400)
		return
	}
	if index, field, ok := validateObjectArray(raw, "alternative_identifiers", map[string]bool{"type": true, "value": true}); !ok {
		out(w, map[string]interface{}{"error": "alternative_identifiers 无效", "item_index": index, "field": field}, 400)
		return
	}
	if index, field, ok := validateObjectArray(raw, "custody_events", custodyEventFields()); !ok {
		out(w, map[string]interface{}{"error": "custody_events 无效", "item_index": index, "field": field}, 400)
		return
	}
	c, e := s.App.CreateWithCustodyRequest(req, x.AccessionCode, x.Title, x.RightsNote, x.CarrierType, x.ContentScope, x.IntakeReceipt, x.CarrierFacets, x.AlternativeIdentifiers, x.CustodyEvents)
	if e != nil {
		if errors.Is(e, domain.ErrConflict) {
			if acc, ne := domain.NormalizeAccession(x.AccessionCode); ne == nil {
				if ex := s.App.Store.FindByAccession(acc); ex != nil {
					out(w, map[string]interface{}{"error": "馆藏号冲突", "case_id": ex.ID, "state": ex.State, "revision": ex.Revision, "case": ex}, 409)
					return
				}
			}
		}
		parseErr(w, e)
		return
	}
	out(w, c, 201)
}

func queryTime(r *http.Request, name string) (*time.Time, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

func jsonContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, "application/json")
}
func (s *Server) caseAction(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 3 {
		out(w, map[string]string{"error": "路径不存在"}, 404)
		return
	}
	id := p[2]
	if r.Method == "GET" {
		if (len(p) == 4 && p[3] == "plan" && strings.EqualFold(r.URL.Query().Get("conflicts"), "true")) || (len(p) == 5 && p[3] == "plan" && p[4] == "conflicts") {
			c, err := s.App.Store.Get(id)
			if err != nil {
				out(w, map[string]string{"error": "未找到个案"}, 404)
				return
			}
			if v := strings.TrimSpace(r.URL.Query().Get("expected_revision")); v != "" {
				if rv, e := strconv.ParseInt(v, 10, 64); e != nil || rv < 1 {
					out(w, map[string]string{"error": "expected_revision 无效"}, 400)
					return
				} else if rv != c.Revision {
					out(w, map[string]interface{}{"error": "版本冲突", "expected_revision": rv, "current_revision": c.Revision}, 409)
					return
				}
			}
			plan := domain.CapturePlan{CaseID: id, PlaybackDevice: strings.TrimSpace(r.URL.Query().Get("playback_device")), Operator: strings.TrimSpace(r.URL.Query().Get("operator"))}
			if plan.PlaybackDevice == "" || plan.Operator == "" {
				out(w, map[string]string{"error": "playback_device 和 operator 为必填项"}, 400)
				return
			}
			plan.ScheduledStart, err = time.Parse(time.RFC3339, r.URL.Query().Get("scheduled_start"))
			if err != nil {
				out(w, map[string]string{"error": "scheduled_start 无效"}, 400)
				return
			}
			plan.ScheduledEnd, err = time.Parse(time.RFC3339, r.URL.Query().Get("scheduled_end"))
			if err != nil || !plan.ScheduledEnd.After(plan.ScheduledStart) {
				out(w, map[string]string{"error": "scheduled_end 无效"}, 400)
				return
			}
			plan.ScheduledStart, plan.ScheduledEnd = plan.ScheduledStart.UTC(), plan.ScheduledEnd.UTC()
			conflicts := s.App.Store.PlanResourceConflicts(id, plan)
			out(w, map[string]interface{}{"case_id": id, "expected_revision": c.Revision, "conflicts": conflicts, "conflict_count": len(conflicts), "playback_device": plan.PlaybackDevice, "operator": plan.Operator, "scheduled_start": plan.ScheduledStart, "scheduled_end": plan.ScheduledEnd, "preflight_digest": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s", plan.PlaybackDevice, plan.Operator, plan.ScheduledStart, plan.ScheduledEnd))))}, 200)
			return
		}
		if len(p) == 4 && p[3] == "captures" {
			if strings.EqualFold(r.URL.Query().Get("lineage"), "true") {
				s.handleCaptureLineage(w, r, id)
			} else {
				s.handleCaptureLedger(w, r, id)
			}
			return
		}
		if len(p) == 4 && p[3] == "recaptures" {
			s.handleRecaptureTimeline(w, r, id)
			return
		}
		if len(p) == 4 && p[3] == "custody" {
			s.handleCustodyChain(w, r, id)
			return
		}
		if len(p) == 5 && p[3] == "assessment" && (p[4] == "history" || p[4] == "diff") {
			s.handleAssessmentHistory(w, r, id)
			return
		}
		if len(p) == 4 && p[3] == "assessment" && strings.EqualFold(r.URL.Query().Get("history"), "true") {
			s.handleAssessmentHistory(w, r, id)
			return
		}
		if len(p) == 5 && p[3] == "quality" && p[4] == "summary" {
			s.handleQualitySummary(w, r, id)
			return
		}
		if len(p) == 4 && p[3] == "audit" {
			s.handleAuditSearch(w, r, id)
			return
		}
		if len(p) == 4 && p[3] == "manifest" {
			if strings.EqualFold(r.URL.Query().Get("preview"), "true") {
				preview, e := s.App.PreviewManifest(id)
				if e != nil {
					parseErr(w, e)
				} else {
					out(w, preview, 200)
				}
				return
			}
			if component := strings.TrimSpace(r.URL.Query().Get("component")); component != "" {
				generation, parseError := componentGeneration(r)
				if parseError != nil {
					out(w, map[string]string{"error": parseError.Error()}, 400)
					return
				}
				proof, proofErr := s.App.ComponentProof(id, component, generation)
				if proofErr != nil {
					parseErr(w, proofErr)
				} else {
					out(w, proof, 200)
				}
				return
			}
			c, e := s.App.Store.Get(id)
			if e != nil {
				out(w, map[string]string{"error": "未找到个案"}, 404)
			} else if c.Manifest == nil {
				out(w, map[string]string{"error": "保存包清单不可用"}, 409)
			} else {
				manifest := *c.Manifest
				if value := strings.TrimSpace(r.URL.Query().Get("generation")); value != "" {
					generation, parseErr := strconv.Atoi(value)
					if parseErr != nil || generation < 1 {
						out(w, map[string]string{"error": "generation 无效"}, 400)
						return
					}
					manifest.GenerationEvidenceIndex = filterGenerationEvidence(manifest.GenerationEvidenceIndex, generation)
					if len(manifest.GenerationEvidenceIndex) == 0 {
						out(w, map[string]string{"error": "未找到该采集代次"}, 404)
						return
					}
				}
				out(w, &manifest, 200)
			}
			return
		}
		if len(p) == 4 && p[3] == "verify" {
			if component := strings.TrimSpace(r.URL.Query().Get("component")); component != "" {
				generation, parseError := componentGeneration(r)
				if parseError != nil {
					out(w, map[string]string{"error": parseError.Error()}, 400)
					return
				}
				proof, proofErr := s.App.ComponentProof(id, component, generation)
				if proofErr != nil {
					parseErr(w, proofErr)
				} else {
					out(w, proof, 200)
				}
				return
			}
			manifest, head, verification, reasons, e := s.App.ManifestVerificationDetails(id)
			if e != nil {
				out(w, map[string]string{"error": "未找到个案"}, 404)
			} else {
				index := []domain.GenerationEvidence{}
				if manifest != nil {
					index = manifest.GenerationEvidenceIndex
				}
				if value := strings.TrimSpace(r.URL.Query().Get("generation")); value != "" {
					generation, parseErr := strconv.Atoi(value)
					if parseErr != nil || generation < 1 {
						out(w, map[string]string{"error": "generation 无效"}, 400)
						return
					}
					index = filterGenerationEvidence(index, generation)
				}
				out(w, map[string]interface{}{"valid": verification.Valid, "status": verification.Status, "manifest": manifest, "audit_head": head, "errors": reasons, "mismatched_components": verification.MismatchedComponents, "reference_errors": verification.ReferenceErrors, "expected_digest": verification.ExpectedDigest, "actual_digest": verification.ActualDigest, "generation_evidence_index": index}, 200)
			}
			return
		}
		if len(p) > 3 {
			out(w, map[string]string{"error": "路径不存在"}, 404)
			return
		}
		c, e := s.App.Store.Get(id)
		if e != nil {
			out(w, map[string]string{"error": "未找到个案"}, 404)
		} else {
			if c.Plan != nil {
				c.Plan.RefreshValidity(time.Now().UTC())
				for i := range c.PlanHistory {
					c.PlanHistory[i].RefreshValidity(time.Now().UTC())
				}
			}
			requirement := c.EscalationStatus()
			if requirement.Required {
				c.CurrentEscalationRequirement = &requirement
			}
			if c.FirstAuditAt.IsZero() {
				c.FirstAuditAt = s.App.Audit.FirstAt(id)
			}
			out(w, c, 200)
		}
		return
	}
	if r.Method != "POST" {
		out(w, map[string]string{"error": "不支持此请求方法"}, 405)
		return
	}
	if len(p) == 5 && p[3] == "plan" && p[4] == "reservation" {
		s.handleReservationRelease(w, r, id)
		return
	}
	if len(p) != 4 {
		out(w, map[string]string{"error": "路径不存在"}, 404)
		return
	}
	if current, err := s.App.Store.Get(id); err != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	} else if current.State == domain.StateSealed {
		parseErr(w, domain.ErrSealed)
		return
	}
	if !jsonContentType(r) {
		out(w, map[string]string{"error": "Content-Type 必须为 application/json"}, 415)
		return
	}
	var raw map[string]json.RawMessage
	if dec(r, &raw) != nil {
		out(w, map[string]string{"error": "请求体无效"}, 400)
		return
	}
	req := requestID(r, raw)
	if req == "" {
		out(w, map[string]string{"error": "request_id 为必填项"}, 400)
		return
	}
	if _, ok := raw["expected_revision"]; !ok {
		out(w, map[string]string{"error": "expected_revision 为必填项"}, 400)
		return
	}
	var rev int64
	if json.Unmarshal(raw["expected_revision"], &rev) != nil || rev <= 0 {
		out(w, map[string]string{"error": "expected_revision 无效"}, 400)
		return
	}
	var e error
	var c *domain.DigitizationCase
	sub := ""
	if len(p) > 3 {
		sub = p[3]
	}
	if sub == "captures" {
		if value, ok := raw["preflight"]; ok {
			var enabled bool
			if json.Unmarshal(value, &enabled) == nil && enabled {
				s.handleCapturePreflight(w, req, id, rev, raw)
				return
			}
		}
	}
	switch sub {
	case "custody":
		normalizeObjectAliases(raw, "transfer", custodyAliases())
		if v, ok := raw["transfer"]; ok {
			var obj map[string]json.RawMessage
			if json.Unmarshal(v, &obj) == nil {
				for k, val := range obj {
					if _, exists := raw[k]; !exists {
						raw[k] = val
					}
				}
				delete(raw, "transfer")
			}
		}
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "transferor": true, "receiver": true, "occurred_at": true, "location_code": true, "seal_status": true, "notes": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		for _, field := range []string{"transferor", "receiver", "occurred_at", "location_code", "seal_status", "notes"} {
			if _, ok := raw[field]; !ok {
				out(w, map[string]string{"error": field + " 为必填项"}, 400)
				return
			}
		}
		var event domain.CustodyEvent
		if json.Unmarshal(rawBytes(raw), &event) != nil {
			out(w, map[string]string{"error": "交接记录无效"}, 400)
			return
		}
		c, e = s.App.CustodyWithRequest(req, id, rev, event)
	case "assessment":
		normalizeAcclimatizationAliases(raw)
		normalizeArrayAliases(raw, "damage_locations", map[string]string{"damage_category": "category", "damage_type": "category", "location": "physical_location", "affected_percentage": "affected_ratio", "affected_proportion": "affected_ratio", "notes": "observation_notes", "observation": "observation_notes", "evidence_digest": "evidence_summary"})
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "case_id": true, "assessor": true, "mold_level": true, "breakage": true, "adhesion": true, "contamination": true, "contamination_notes": true, "playback_risk": true, "required_treatment": true, "treatment_evidence": true, "no_treatment_required": true, "observation_evidence": true, "acclimatization": true, "assessment_version": true, "correction_reason": true, "damage_locations": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		for _, field := range []string{"assessor", "mold_level", "breakage", "adhesion", "contamination", "playback_risk"} {
			if _, ok := raw[field]; !ok {
				out(w, map[string]string{"error": field + " 为必填项"}, 400)
				return
			}
		}
		if _, correction := raw["assessment_version"]; correction {
			if _, ok := raw["correction_reason"]; !ok {
				out(w, map[string]string{"error": "correction_reason 为必填项"}, 400)
				return
			}
		}
		if index, field, ok := validateObjectArray(raw, "treatment_evidence", map[string]bool{"category": true, "action": true, "performed_by": true, "completed_at": true, "evidence_summary": true}); !ok {
			out(w, map[string]interface{}{"error": "treatment_evidence 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "observation_evidence", map[string]bool{"risk_category": true, "evidence_type": true, "asset_digest": true, "observed_at": true, "recorded_by": true, "description": true}); !ok {
			out(w, map[string]interface{}{"error": "observation_evidence 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "damage_locations", map[string]bool{"facet_id": true, "category": true, "physical_location": true, "severity": true, "affected_ratio": true, "observation_notes": true, "evidence_summary": true}); !ok {
			out(w, map[string]interface{}{"error": "damage_locations 无效", "item_index": index, "field": field}, 400)
			return
		}
		if value, exists := raw["acclimatization"]; exists {
			if ok, field := validateNestedObject(value, map[string]bool{"required": true, "minimum_temperature_c": true, "maximum_temperature_c": true, "minimum_relative_humidity": true, "maximum_relative_humidity": true, "minimum_stable_duration_minutes": true, "readings": true}, "readings", map[string]bool{"measured_at": true, "temperature_c": true, "relative_humidity": true, "measured_by": true, "instrument_id": true}); !ok {
				out(w, map[string]string{"error": "acclimatization 无效", "field": field}, 400)
				return
			}
		}
		var x domain.ConditionAssessment
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		c, e = s.App.AssessWithRequest(req, id, rev, x)
	case "plan":
		if value, ok := raw["reapproval_reason"]; ok {
			if _, exists := raw["revision_reason"]; !exists {
				raw["revision_reason"] = value
			}
		}
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "case_id": true, "playback_device": true, "signal_chain": true, "target_codec": true, "sample_rate_hz": true, "bit_depth": true, "channel_map": true, "operator": true, "approved_by": true, "revision_reason": true, "reapproval_reason": true, "valid_until": true, "risk_controls": true, "no_additional_controls": true, "capture_tasks": true, "skipped_facets": true, "scheduled_start": true, "scheduled_end": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		for _, field := range []string{"playback_device", "signal_chain", "target_codec", "sample_rate_hz", "bit_depth", "channel_map", "operator", "approved_by"} {
			if _, ok := raw[field]; !ok {
				out(w, map[string]string{"error": field + " 为必填项"}, 400)
				return
			}
		}
		var x domain.CapturePlan
		if index, field, ok := validateObjectArray(raw, "risk_controls", map[string]bool{"risk_category": true, "control_category": true, "operational_measure": true, "responsible_person": true, "pre_capture_check": true}); !ok {
			out(w, map[string]interface{}{"error": "risk_controls 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "capture_tasks", map[string]bool{"task_id": true, "facet_id": true, "execution_order": true, "estimated_duration_ms": true, "content_start": true, "content_end": true, "channel_map": true}); !ok {
			out(w, map[string]interface{}{"error": "capture_tasks 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "skipped_facets", map[string]bool{"facet_id": true, "reason": true}); !ok {
			out(w, map[string]interface{}{"error": "skipped_facets 无效", "item_index": index, "field": field}, 400)
			return
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		c, e = s.App.PlanWithRequest(req, id, rev, x)
	case "captures":
		if itemsRaw, grouped := raw["items"]; grouped {
			if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "case_id": true, "generation": true, "plan_revision": true, "plan_fingerprint": true, "recapture_authorization_version": true, "items": true}) {
				out(w, map[string]string{"error": "存在未知字段"}, 400)
				return
			}
			var entries []map[string]json.RawMessage
			if json.Unmarshal(itemsRaw, &entries) != nil || len(entries) == 0 {
				out(w, map[string]string{"error": "items 无效"}, 400)
				return
			}
			items := make([]domain.CaptureGeneration, len(entries))
			for index, fields := range entries {
				normalizeArrayAliases(fields, "file_segments", fileSegmentAliases())
				if !expandFixityObject(fields) || unknown(fields, captureItemFields()) {
					out(w, map[string]interface{}{"error": "采集项目存在未知字段", "item_index": index}, 400)
					return
				}
				if field, ok := validateCaptureEvidenceFields(fields); !ok {
					out(w, map[string]interface{}{"error": "采集项目证据无效", "item_index": index, "field": field}, 400)
					return
				}
				for _, shared := range []string{"generation", "plan_revision", "plan_fingerprint", "recapture_authorization_version"} {
					if _, exists := fields[shared]; !exists {
						if value, ok := raw[shared]; ok {
							fields[shared] = value
						}
					}
				}
				if itemIndex, field, ok := validateObjectArray(fields, "calibration_measurements", calibrationMeasurementFields()); !ok {
					out(w, map[string]interface{}{"error": "calibration_measurements 无效", "item_index": index, "measurement_index": itemIndex, "field": field}, 400)
					return
				}
				if json.Unmarshal(rawBytes(fields), &items[index]) != nil {
					out(w, map[string]interface{}{"error": "采集项目无效", "item_index": index}, 400)
					return
				}
			}
			c, e = s.App.CaptureGroupWithRequest(req, id, rev, items)
			break
		}
		if !expandFixityObject(raw) {
			out(w, map[string]string{"error": "fixity_chunks 无效"}, 400)
			return
		}
		normalizeArrayAliases(raw, "file_segments", fileSegmentAliases())
		allowedCapture := captureItemFields()
		allowedCapture["request_id"], allowedCapture["expected_revision"], allowedCapture["case_id"] = true, true, true
		if unknown(raw, allowedCapture) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		for _, field := range []string{"calibration_reference", "calibration_device", "calibrated_at", "calibration_valid_until", "started_at", "ended_at", "asset_digest", "asset_size_bytes", "container_format", "actual_codec", "actual_sample_rate_hz", "actual_bit_depth", "actual_channels", "duration_ms", "peak_dbfs", "plan_revision"} {
			if _, ok := raw[field]; !ok {
				out(w, map[string]string{"error": field + " 为必填项"}, 400)
				return
			}
		}
		var x domain.CaptureGeneration
		if index, field, ok := validateObjectArray(raw, "operation_events", map[string]bool{"type": true, "occurred_at": true, "operator": true, "description": true}); !ok {
			out(w, map[string]interface{}{"error": "operation_events 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "fixity_chunks", map[string]bool{"index": true, "size_bytes": true, "digest": true}); !ok {
			out(w, map[string]interface{}{"error": "fixity_chunks 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "calibration_measurements", calibrationMeasurementFields()); !ok {
			out(w, map[string]interface{}{"error": "calibration_measurements 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "file_segments", fileSegmentFields()); !ok {
			out(w, map[string]interface{}{"error": "file_segments 无效", "segment_index": index, "field": field}, 400)
			return
		}
		if value, exists := raw["calibration_profile"]; exists {
			if ok, field := validateNestedObject(value, map[string]bool{"reference_frequency_hz": true, "frequency_tolerance_hz": true, "level_tolerance_db": true, "channel_difference_db": true}, "", nil); !ok {
				out(w, map[string]string{"error": "calibration_profile 无效", "field": field}, 400)
				return
			}
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		if len(strings.TrimSpace(x.AssetDigest)) != 64 {
			out(w, map[string]string{"error": "asset_digest 无效"}, 400)
			return
		}
		c, e = s.App.CaptureWithRequest(req, id, rev, x)
	case "quality":
		normalizeObjectAliases(raw, "measurement_profile", map[string]string{"parameter_config_digest": "parameters_digest", "configuration_digest": "parameters_digest"})
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "case_id": true, "generation": true, "completeness_passed": true, "clipping_events": true, "dropout_events": true, "channel_mapping_passed": true, "duration_variance_ms": true, "listening_notes": true, "reviewer": true, "decision": true, "defect_markers": true, "listening_intervals": true, "remediation_checks": true, "channel_metrics": true, "measurement_profile": true, "countersign_for_revision": true, "confirmed_evidence_digest": true, "adjudication_for_revision": true, "adjudicator": true, "disagreement_resolutions": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		var x domain.QualityDecision
		requiredQualityFields := []string{"generation", "completeness_passed", "clipping_events", "dropout_events", "channel_mapping_passed", "duration_variance_ms", "listening_notes", "reviewer", "defect_markers"}
		if _, countersign := raw["countersign_for_revision"]; countersign {
			requiredQualityFields = []string{"generation", "listening_notes", "reviewer", "decision", "confirmed_evidence_digest", "countersign_for_revision"}
		}
		if _, adjudication := raw["adjudication_for_revision"]; adjudication {
			requiredQualityFields = []string{"generation", "listening_notes", "decision", "confirmed_evidence_digest", "adjudication_for_revision", "countersign_for_revision", "disagreement_resolutions"}
			if _, ok := raw["adjudicator"]; !ok {
				requiredQualityFields = append(requiredQualityFields, "reviewer")
			}
		}
		for _, field := range requiredQualityFields {
			if _, ok := raw[field]; !ok {
				out(w, map[string]string{"error": field + " 为必填项"}, 400)
				return
			}
		}
		if index, field, ok := validateObjectArray(raw, "defect_markers", map[string]bool{"defect_type": true, "position_ms": true, "duration_ms": true, "channel": true, "description": true, "severity": true}); !ok {
			out(w, map[string]interface{}{"error": "defect_markers 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "listening_intervals", map[string]bool{"start_ms": true, "end_ms": true, "channel": true, "method": true}); !ok {
			out(w, map[string]interface{}{"error": "listening_intervals 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "remediation_checks", map[string]bool{"category": true, "result": true, "verified_by": true, "evidence_description": true}); !ok {
			out(w, map[string]interface{}{"error": "remediation_checks 无效", "item_index": index, "field": field}, 400)
			return
		}
		if index, field, ok := validateObjectArray(raw, "channel_metrics", map[string]bool{"channel": true, "dc_offset": true, "integrated_loudness_lufs": true, "noise_floor_dbfs": true, "silence_ratio": true}); !ok {
			out(w, map[string]interface{}{"error": "channel_metrics 无效", "item_index": index, "field": field}, 400)
			return
		}
		if value, exists := raw["measurement_profile"]; exists {
			if ok, field := validateNestedObject(value, map[string]bool{"tool": true, "tool_version": true, "parameters_digest": true}, "", nil); !ok {
				out(w, map[string]string{"error": "measurement_profile 无效", "field": field}, 400)
				return
			}
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil || x.ListeningNotes == "" || (x.Reviewer == "" && x.Adjudicator == "") || x.ClippingEvents < 0 || x.DropoutEvents < 0 {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		c, e = s.App.QualityWithRequest(req, id, rev, x)
	case "recaptures":
		normalizeArrayAliases(raw, "remediations", map[string]string{"executed_by": "performed_by", "executor": "performed_by", "completed_by": "performed_by", "execution_completed_at": "completed_at", "execution_result": "result", "execution_outcome": "result", "evidence_summary": "evidence_digest", "verification": "verification_method"})
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "action": true, "reason": true, "remediation": true, "failed_generation": true, "remediations": true, "authorized_by": true, "authorized_at": true, "expires_at": true, "authorization_version": true, "revoked_by": true, "revoked_at": true, "revocation_reason": true, "escalation": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		var x domain.RecaptureAction
		if index, field, ok := validateObjectArray(raw, "remediations", map[string]bool{"category": true, "action": true, "owner": true, "completion_criteria": true, "performed_by": true, "completed_at": true, "result": true, "evidence_digest": true, "verification_method": true}); !ok {
			out(w, map[string]interface{}{"error": "remediations 无效", "item_index": index, "field": field}, 400)
			return
		}
		if value, exists := raw["escalation"]; exists {
			if ok, field := validateNestedObject(value, map[string]bool{"required": true, "triggered_categories": true, "failure_generations": true, "preservation_officer": true, "risk_disposition": true, "maximum_additional_attempts": true, "remaining_attempts": true}, "", nil); !ok {
				out(w, map[string]string{"error": "escalation 无效", "field": field}, 400)
				return
			}
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		c, e = s.App.RecaptureWithRequest(req, id, rev, x)
	case "seal":
		if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "sealed_by": true, "expected_manifest_digest": true}) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		var x struct {
			SealedBy               string `json:"sealed_by"`
			ExpectedManifestDigest string `json:"expected_manifest_digest"`
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		if strings.TrimSpace(x.SealedBy) == "" || strings.TrimSpace(x.ExpectedManifestDigest) == "" {
			out(w, map[string]string{"error": "sealed_by 和 expected_manifest_digest 为必填项"}, 400)
			return
		}
		c, e = s.App.SealWithDigestRequest(req, id, rev, x.SealedBy, x.ExpectedManifestDigest)
	default:
		out(w, map[string]string{"error": "路径不存在"}, 404)
		return
	}
	if e != nil {
		parseErr(w, e)
		return
	}
	extra := map[string]interface{}{}
	switch sub {
	case "custody":
		extra["current_custodian"] = c.CurrentCustodian
		extra["current_location_code"] = c.CurrentLocationCode
		extra["custody_chain_digest"] = c.CustodyChainDigest
	case "assessment":
		extra["risk_summary"] = c.Assessment.RiskSummary
		extra["risk_categories"] = c.Assessment.RiskCategories
		extra["observation_evidence_digest"] = c.Assessment.ObservationEvidenceDigest
		extra["acclimatization"] = c.Assessment.Acclimatization
		extra["damage_locations"] = c.Assessment.DamageLocations
		extra["damage_summaries"] = c.Assessment.DamageSummaries
		extra["damage_location_digest"] = c.Assessment.DamageLocationDigest
	case "plan":
		extra["plan_fingerprint"] = c.Plan.Fingerprint
		extra["plan_revision"] = c.Plan.PlanRevision
		extra["changed_fields"] = c.Plan.ChangedFields
		extra["risk_control_digest"] = c.Plan.RiskControlDigest
		extra["covered_risk_categories"] = c.Plan.CoveredRiskCategories
		extra["capture_tasks"] = c.Plan.CaptureTasks
		extra["skipped_facets"] = c.Plan.SkippedFacets
		extra["task_coverage_digest"] = c.Plan.TaskCoverageDigest
		extra["estimated_total_duration_ms"] = c.Plan.EstimatedTotalDurationMs
		c.Plan.RefreshValidity(time.Now().UTC())
		extra["valid_until"] = c.Plan.ValidUntil
		extra["validity_status"] = c.Plan.ValidityStatus
		extra["reapproves_plan_revision"] = c.Plan.ReapprovesPlanRevision
		extra["scheduled_start"] = c.Plan.ScheduledStart
		extra["scheduled_end"] = c.Plan.ScheduledEnd
		extra["reservation_status"] = c.Plan.ReservationStatus
		if len(c.Plan.ChangedFields) > 0 {
			extra["previous_plan_revision"] = c.Plan.PlanRevision - 1
		}
	case "captures":
		extra["generation"] = c.CurrentCaptureGeneration
		currentItems := []domain.CaptureGeneration{}
		for _, capture := range c.Captures {
			if capture.Generation == c.CurrentCaptureGeneration {
				currentItems = append(currentItems, capture)
			}
		}
		last := c.Captures[len(c.Captures)-1]
		extra["items"] = currentItems
		extra["calibration_valid_until"] = last.CalibrationValidUntil
		extra["technical_evidence_digest"] = last.TechnicalEvidenceDigest
		extra["operation_timeline_digest"] = c.Captures[len(c.Captures)-1].OperationTimelineDigest
		extra["paused_duration_ms"] = c.Captures[len(c.Captures)-1].PausedDurationMs
		extra["calculated_audio_duration_ms"] = c.Captures[len(c.Captures)-1].CalculatedAudioDurationMs
		extra["capture_task_id"] = c.Captures[len(c.Captures)-1].CaptureTaskID
		extra["fixity_digest"] = c.Captures[len(c.Captures)-1].FixityDigest
		extra["fixity_combination_rule"] = last.FixityCombinationRule
		extra["calibration_status"] = last.CalibrationStatus
		extra["calibration_policy_version"] = last.CalibrationPolicyVersion
		extra["calibration_evidence_digest"] = last.CalibrationEvidenceDigest
		extra["calibration_results"] = last.CalibrationResults
		extra["file_segments"] = last.FileSegments
		extra["segment_combination_rule"] = last.SegmentCombinationRule
	case "quality":
		q := c.Quality[len(c.Quality)-1]
		extra["decision"] = q.Decision
		extra["failure_categories"] = q.FailureCategories
		extra["failure_summary"] = q.FailureSummary
		extra["defect_markers"] = q.DefectMarkers
		extra["defect_summary"] = q.DefectSummary
		extra["defect_impacts"] = q.DefectImpacts
		extra["defect_impact_digest"] = q.DefectImpactDigest
		extra["listening_coverage"] = q.ListeningCoverage
		extra["listening_coverage_digest"] = q.ListeningCoverageDigest
		extra["remediation_effect_digest"] = q.RemediationEffectDigest
		extra["resolved_categories"] = q.ResolvedCategories
		extra["persistent_categories"] = q.PersistentCategories
		extra["new_categories"] = q.NewCategories
		extra["channel_metrics"] = q.ChannelMetrics
		extra["metric_results"] = q.MetricResults
		extra["threshold_version"] = q.ThresholdVersion
		extra["quality_evidence_digest"] = q.QualityEvidenceDigest
		extra["quality_revision"] = q.QualityRevision
		extra["requires_countersign"] = q.RequiresCountersign
		extra["countersign_reasons"] = q.CountersignReasons
		extra["countersign_status"] = q.CountersignStatus
		if len(q.Countersigns) > 0 {
			extra["countersign"] = q.Countersigns[len(q.Countersigns)-1]
			extra["disagreements"] = q.Countersigns[len(q.Countersigns)-1].Disagreements
		}
		if q.Adjudication != nil {
			extra["adjudication"] = q.Adjudication
		}
	case "seal":
		extra["canonical_payload_digest"] = c.Manifest.CanonicalPayloadDigest
		extra["verification_status"] = c.Manifest.VerificationStatus
	}
	outCase(w, c, extra, 200)
}

func outCase(w http.ResponseWriter, c *domain.DigitizationCase, extra map[string]interface{}, code int) {
	b, _ := json.Marshal(c)
	var response map[string]interface{}
	_ = json.Unmarshal(b, &response)
	for key, value := range extra {
		response[key] = value
	}
	out(w, response, code)
}
func requestID(r *http.Request, raw map[string]json.RawMessage) string {
	if v := r.Header.Get("X-Request-ID"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	var x string
	_ = json.Unmarshal(raw["request_id"], &x)
	return strings.TrimSpace(x)
}
func unknown(raw map[string]json.RawMessage, allowed map[string]bool) bool {
	for k := range raw {
		if !allowed[k] {
			return true
		}
	}
	return false
}
func rawBytes(m map[string]json.RawMessage) []byte { b, _ := json.Marshal(m); return b }
func filterGenerationEvidence(items []domain.GenerationEvidence, generation int) []domain.GenerationEvidence {
	result := []domain.GenerationEvidence{}
	for _, item := range items {
		if item.Generation == generation {
			result = append(result, item)
		}
	}
	return result
}
func componentGeneration(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("generation"))
	if value == "" {
		return 0, nil
	}
	generation, err := strconv.Atoi(value)
	if err != nil || generation < 1 {
		return 0, errors.New("generation 无效")
	}
	return generation, nil
}
func captureItemFields() map[string]bool {
	return map[string]bool{"generation": true, "calibration_reference": true, "calibration_device": true, "calibrated_at": true, "calibration_valid_until": true, "started_at": true, "ended_at": true, "asset_digest": true, "asset_size_bytes": true, "container_format": true, "actual_codec": true, "actual_sample_rate_hz": true, "actual_bit_depth": true, "actual_channels": true, "duration_ms": true, "peak_dbfs": true, "plan_revision": true, "plan_fingerprint": true, "operation_events": true, "capture_task_id": true, "fixity_algorithm": true, "fixity_chunk_size_bytes": true, "fixity_chunks": true, "recapture_authorization_version": true, "recapture_remediation_digest": true, "calibration_measurements": true, "calibration_profile": true, "file_segments": true}
}
func calibrationMeasurementFields() map[string]bool {
	return map[string]bool{"channel": true, "reference_frequency_hz": true, "measured_frequency_hz": true, "target_level_dbfs": true, "measured_level_dbfs": true, "measured_at": true, "instrument_id": true, "instrument_certificate_digest": true}
}
func validateCaptureEvidenceFields(raw map[string]json.RawMessage) (string, bool) {
	checks := []struct {
		name   string
		fields map[string]bool
	}{
		{"operation_events", map[string]bool{"type": true, "occurred_at": true, "operator": true, "description": true}},
		{"fixity_chunks", map[string]bool{"index": true, "size_bytes": true, "digest": true}},
		{"calibration_measurements", calibrationMeasurementFields()},
		{"file_segments", fileSegmentFields()},
	}
	for _, check := range checks {
		if index, field, ok := validateObjectArray(raw, check.name, check.fields); !ok {
			return check.name + "[" + strconv.Itoa(index) + "]." + field, false
		}
	}
	if value, exists := raw["calibration_profile"]; exists {
		if ok, field := validateNestedObject(value, map[string]bool{"reference_frequency_hz": true, "frequency_tolerance_hz": true, "level_tolerance_db": true, "channel_difference_db": true}, "", nil); !ok {
			return "calibration_profile." + field, false
		}
	}
	return "", true
}

func fileSegmentFields() map[string]bool {
	return map[string]bool{"segment_index": true, "source_start_ms": true, "source_end_ms": true, "duration_ms": true, "asset_size_bytes": true, "asset_digest": true, "starts_continuous": true, "ends_continuous": true}
}

func fileSegmentAliases() map[string]string {
	return map[string]string{"sequence": "segment_index", "index": "segment_index", "segment_no": "segment_index", "source_start_position_ms": "source_start_ms", "source_start": "source_start_ms", "source_end_position_ms": "source_end_ms", "source_end": "source_end_ms", "size_bytes": "asset_size_bytes", "digest": "asset_digest", "head_continuity": "starts_continuous", "start_continuity": "starts_continuous", "tail_continuity": "ends_continuous", "end_continuity": "ends_continuous"}
}

func custodyAliases() map[string]string {
	return map[string]string{"released_by": "transferor", "handed_over_by": "transferor", "from_custodian": "transferor", "from_person": "transferor", "received_by": "receiver", "to_custodian": "receiver", "to_person": "receiver", "event_at": "occurred_at", "custody_at": "occurred_at", "handover_at": "occurred_at", "storage_location": "location_code", "normalized_location_code": "location_code", "packaging_seal_status": "seal_status", "package_seal_status": "seal_status", "handover_notes": "notes", "remark": "notes", "description": "notes"}
}

func custodyEventFields() map[string]bool {
	return map[string]bool{"transferor": true, "receiver": true, "occurred_at": true, "location_code": true, "seal_status": true, "notes": true}
}

func normalizeArrayAliases(raw map[string]json.RawMessage, name string, aliases map[string]string) {
	value, ok := raw[name]
	if !ok {
		return
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(value, &entries) != nil {
		return
	}
	for _, entry := range entries {
		for alias, canonical := range aliases {
			if v, exists := entry[alias]; exists {
				if _, set := entry[canonical]; !set {
					entry[canonical] = v
				}
				delete(entry, alias)
			}
		}
	}
	raw[name], _ = json.Marshal(entries)
}
func normalizeObjectAliases(raw map[string]json.RawMessage, name string, aliases map[string]string) {
	value, ok := raw[name]
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil {
		return
	}
	for alias, canonical := range aliases {
		if v, exists := fields[alias]; exists {
			if _, set := fields[canonical]; !set {
				fields[canonical] = v
			}
			delete(fields, alias)
		}
	}
	raw[name], _ = json.Marshal(fields)
}
func normalizeAcclimatizationAliases(raw map[string]json.RawMessage) {
	value, ok := raw["acclimatization"]
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil {
		return
	}
	aliases := map[string]string{"temperature_min_c": "minimum_temperature_c", "temperature_max_c": "maximum_temperature_c", "relative_humidity_min_percent": "minimum_relative_humidity", "relative_humidity_max_percent": "maximum_relative_humidity", "minimum_stable_minutes": "minimum_stable_duration_minutes"}
	for alias, canonical := range aliases {
		if v, exists := fields[alias]; exists {
			if _, set := fields[canonical]; !set {
				fields[canonical] = v
			}
			delete(fields, alias)
		}
	}
	if readings, exists := fields["readings"]; exists {
		var entries []map[string]json.RawMessage
		if json.Unmarshal(readings, &entries) == nil {
			for _, entry := range entries {
				if v, present := entry["relative_humidity_percent"]; present {
					if _, set := entry["relative_humidity"]; !set {
						entry["relative_humidity"] = v
					}
					delete(entry, "relative_humidity_percent")
				}
			}
			fields["readings"], _ = json.Marshal(entries)
		}
	}
	raw["acclimatization"], _ = json.Marshal(fields)
}
func expandFixityObject(raw map[string]json.RawMessage) bool {
	value, ok := raw["fixity_chunks"]
	if !ok {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) != nil {
		return true
	}
	if unknown(object, map[string]bool{"algorithm": true, "chunk_size_bytes": true, "asset_size_bytes": true, "chunks": true}) {
		return false
	}
	chunks, ok := object["chunks"]
	if !ok {
		return false
	}
	raw["fixity_chunks"] = chunks
	if v, exists := object["algorithm"]; exists {
		raw["fixity_algorithm"] = v
	}
	if v, exists := object["chunk_size_bytes"]; exists {
		raw["fixity_chunk_size_bytes"] = v
	}
	if v, exists := object["asset_size_bytes"]; exists {
		if existing, set := raw["asset_size_bytes"]; set && string(existing) != string(v) {
			return false
		}
		raw["asset_size_bytes"] = v
	}
	return true
}

func validateNestedObject(value json.RawMessage, allowed map[string]bool, arrayName string, arrayAllowed map[string]bool) (bool, string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil || unknown(fields, allowed) {
		return false, ""
	}
	if arrayName != "" {
		if _, exists := fields[arrayName]; exists {
			if index, field, ok := validateObjectArray(fields, arrayName, arrayAllowed); !ok {
				return false, arrayName + "[" + strconv.Itoa(index) + "]." + field
			}
		}
	}
	return true, ""
}
func parseErr(w http.ResponseWriter, e error) {
	code := 400
	if errors.Is(e, domain.ErrNotFound) {
		code = 404
	}
	if errors.Is(e, domain.ErrConflict) {
		code = 409
	}
	if errors.Is(e, domain.ErrIntegrity) || errors.Is(e, domain.ErrState) || errors.Is(e, domain.ErrSealed) {
		code = 409
	}
	if errors.Is(e, domain.ErrPersistence) {
		code = http.StatusInternalServerError
	}
	response := map[string]interface{}{"error": e.Error()}
	var detail *domain.DetailError
	if errors.As(e, &detail) {
		for key, value := range detail.Details {
			response[key] = value
		}
	}
	out(w, response, code)
}
