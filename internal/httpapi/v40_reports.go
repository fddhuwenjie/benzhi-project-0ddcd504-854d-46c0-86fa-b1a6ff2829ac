package httpapi

import (
	"archiveflow/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type recaptureTimelineProjection struct {
	CaseID            string                      `json:"case_id"`
	Timeline          []map[string]interface{}    `json:"timeline"`
	Items             []map[string]interface{}    `json:"items"`
	RemainingAttempts int                         `json:"remaining_attempts"`
	Escalation        *domain.RecaptureEscalation `json:"escalation,omitempty"`
	AuditHead         string                      `json:"audit_head"`
	Revision          int64                       `json:"revision"`
}

func (s *Server) handleAssessmentHistory(w http.ResponseWriter, r *http.Request, id string) {
	c, e := s.App.Store.Get(id)
	if e != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	}
	hist := append([]domain.ConditionAssessment(nil), c.AssessmentHistory...)
	if c.Assessment != nil {
		hist = append(hist, *c.Assessment)
	}
	sort.Slice(hist, func(i, j int) bool { return hist[i].AssessmentVersion < hist[j].AssessmentVersion })
	if len(hist) == 0 {
		out(w, map[string]string{"error": "不存在评估版本"}, 404)
		return
	}
	parse := func(k string) (int, error) {
		v := r.URL.Query().Get(k)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, err
		}
		return n, nil
	}
	from, err := parse("from_version")
	if err != nil {
		out(w, map[string]string{"error": "from_version 无效"}, 400)
		return
	}
	to, err := parse("to_version")
	if err != nil {
		out(w, map[string]string{"error": "to_version 无效"}, 400)
		return
	}
	single, err := parse("assessment_version")
	if err != nil {
		out(w, map[string]string{"error": "assessment_version 无效"}, 400)
		return
	}
	if single > 0 {
		from, to = single, single
	}
	if from == 0 {
		from = hist[0].AssessmentVersion
	}
	if to == 0 {
		to = hist[len(hist)-1].AssessmentVersion
	}
	if from > to {
		out(w, map[string]string{"error": "from_version 不能大于 to_version"}, 400)
		return
	}
	var before, after *domain.ConditionAssessment
	for i := range hist {
		if hist[i].AssessmentVersion == from {
			before = &hist[i]
		}
		if hist[i].AssessmentVersion == to {
			after = &hist[i]
		}
	}
	if before == nil || after == nil {
		out(w, map[string]string{"error": "不存在 assessment_version"}, 404)
		return
	}
	changes := map[string]interface{}{}
	setDiff := func(a, b []string) ([]string, []string) {
		ma, mb := map[string]bool{}, map[string]bool{}
		for _, v := range a {
			ma[v] = true
		}
		for _, v := range b {
			mb[v] = true
		}
		add, rem := []string{}, []string{}
		for v := range mb {
			if !ma[v] {
				add = append(add, v)
			}
		}
		for v := range ma {
			if !mb[v] {
				rem = append(rem, v)
			}
		}
		sort.Strings(add)
		sort.Strings(rem)
		return add, rem
	}
	added, removed := setDiff(before.RiskCategories, after.RiskCategories)
	changes["risk_categories"] = map[string]interface{}{"added": added, "removed": removed}
	compare := func(name string, a, b interface{}) {
		if !reflect.DeepEqual(a, b) {
			changes[name] = map[string]interface{}{"from": a, "to": b}
		}
	}
	compare("playback_risk", before.PlaybackRisk, after.PlaybackRisk)
	compare("damage_location_digest", before.DamageLocationDigest, after.DamageLocationDigest)
	compare("treatment_coverage", before.TreatmentCoverage, after.TreatmentCoverage)
	var ar, br string
	if before.Acclimatization != nil {
		ar = before.Acclimatization.ReleaseDecision
	}
	if after.Acclimatization != nil {
		br = after.Acclimatization.ReleaseDecision
	}
	compare("acclimatization", ar, br)
	compare("observation_evidence_digest", before.ObservationEvidenceDigest, after.ObservationEvidenceDigest)
	b, _ := json.Marshal(changes)
	h := sha256.Sum256(b)
	integrity := "ok"
	if !s.App.Audit.Validate(id, c.Revision) {
		integrity = "integrity_error"
	}
	out(w, map[string]interface{}{"case_id": id, "from_version": from, "to_version": to, "before": before, "after": after, "changes": changes, "diff_digest": hex.EncodeToString(h[:]), "pending_categories": after.RiskCategories, "blocking_reasons": after.RiskCategories, "integrity_status": integrity, "audit_head": s.App.Audit.Head(id), "revision": c.Revision}, 200)
}

func (s *Server) handleCaptureLineage(w http.ResponseWriter, r *http.Request, id string) {
	c, e := s.App.Store.Get(id)
	if e != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	}
	gen := 0
	if v := r.URL.Query().Get("generation"); v != "" {
		gen, _ = strconv.Atoi(v)
		if gen < 1 {
			out(w, map[string]string{"error": "generation 无效"}, 400)
			return
		}
	}
	task := r.URL.Query().Get("capture_task_id")
	type row struct {
		Generation              int                      `json:"generation"`
		CaptureTaskID           string                   `json:"capture_task_id"`
		AssetDigest             string                   `json:"asset_digest"`
		PlanRevision            int64                    `json:"plan_revision"`
		PlanFingerprint         string                   `json:"plan_fingerprint"`
		CalibrationReference    string                   `json:"calibration_reference"`
		TechnicalEvidenceDigest string                   `json:"technical_evidence_digest"`
		FixityDigest            string                   `json:"fixity_digest"`
		Status                  string                   `json:"status"`
		Alerts                  []string                 `json:"alerts,omitempty"`
		DuplicateOf             map[string]interface{}   `json:"duplicate_of,omitempty"`
		Capture                 domain.CaptureGeneration `json:"capture"`
	}
	rows := []row{}
	seen := map[string]row{}
	for _, g := range c.Captures {
		if gen > 0 && g.Generation != gen {
			continue
		}
		if task != "" && g.CaptureTaskID != task {
			continue
		}
		if c.Plan != nil && g.PlanRevision != c.Plan.PlanRevision { /* keep evidence readable while flagging mismatch */
		}
		status := "verified"
		alerts := []string{}
		if c.Plan != nil && g.PlanRevision != c.Plan.PlanRevision {
			status = "plan_mismatch"
			alerts = append(alerts, "plan_mismatch")
		}
		if g.TechnicalEvidenceDigest == "" || g.TechnicalEvidenceDigest != domain.CaptureTechnicalDigest(g) {
			status = "integrity_error"
			alerts = append(alerts, "technical_evidence")
		}
		key := g.AssetDigest
		dup := map[string]interface{}(nil)
		if p, ok := seen[key]; ok && key != "" {
			status = "duplicate_asset"
			alerts = append(alerts, "duplicate_asset")
			dup = map[string]interface{}{"generation": p.Generation, "capture_task_id": p.CaptureTaskID}
		}
		seen[key] = row{Generation: g.Generation, CaptureTaskID: g.CaptureTaskID}
		rows = append(rows, row{Generation: g.Generation, CaptureTaskID: g.CaptureTaskID, AssetDigest: g.AssetDigest, PlanRevision: g.PlanRevision, PlanFingerprint: g.PlanFingerprint, CalibrationReference: g.CalibrationReference, TechnicalEvidenceDigest: g.TechnicalEvidenceDigest, FixityDigest: g.FixityDigest, Status: status, Alerts: alerts, DuplicateOf: dup, Capture: g})
	}
	if c.Plan != nil {
		for _, t := range c.Plan.CaptureTasks {
			found := false
			for _, x := range rows {
				if x.Generation == c.CurrentCaptureGeneration && x.CaptureTaskID == t.TaskID {
					found = true
					break
				}
			}
			if !found && (gen == 0 || gen == c.CurrentCaptureGeneration) {
				rows = append(rows, row{Generation: c.CurrentCaptureGeneration, CaptureTaskID: t.TaskID, PlanRevision: c.Plan.PlanRevision, Status: "missing", Alerts: []string{"missing"}})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Generation != rows[j].Generation {
			return rows[i].Generation < rows[j].Generation
		}
		return rows[i].CaptureTaskID < rows[j].CaptureTaskID
	})
	b, _ := json.Marshal(rows)
	h := sha256.Sum256(b)
	status := "verified"
	for _, x := range rows {
		if x.Status != "verified" {
			status = x.Status
			break
		}
	}
	if !s.App.Audit.Validate(id, c.Revision) {
		status = "integrity_error"
	}
	out(w, map[string]interface{}{"case_id": id, "lineage": rows, "items": rows, "lineage_digest": hex.EncodeToString(h[:]), "lineage_status": status, "audit_head": s.App.Audit.Head(id), "revision": c.Revision}, 200)
}

func (s *Server) handleRecaptureTimeline(w http.ResponseWriter, r *http.Request, id string) {
	statusFilter := strings.ToUpper(r.URL.Query().Get("status"))
	genFilter, authFilter := 0, 0
	if v := r.URL.Query().Get("generation"); v != "" {
		genFilter, _ = strconv.Atoi(v)
		if genFilter < 1 {
			out(w, map[string]string{"error": "generation 无效"}, 400)
			return
		}
	}
	if v := r.URL.Query().Get("authorization_version"); v != "" {
		authFilter, _ = strconv.Atoi(v)
		if authFilter < 1 {
			out(w, map[string]string{"error": "authorization_version 无效"}, 400)
			return
		}
	}
	valid := map[string]bool{"": true, "ACTIVE": true, "EXPIRED": true, "REVOKED": true, "CONSUMED": true, "SUPERSEDED": true}
	if !valid[statusFilter] {
		out(w, map[string]string{"error": "status 无效"}, 400)
		return
	}
	asOf := time.Now().UTC()
	if v := r.URL.Query().Get("as_of"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			out(w, map[string]string{"error": "as_of 无效"}, 400)
			return
		}
		asOf = t
	}
	cacheKey := id + "?" + r.URL.RawQuery
	s.recaptureTimelineMu.Lock()
	if cached, ok := s.recaptureTimelines[cacheKey]; ok {
		s.recaptureTimelineMu.Unlock()
		out(w, cached, 200)
		return
	}
	s.recaptureTimelineMu.Unlock()
	c, e := s.App.Store.Get(id)
	if e != nil {
		out(w, map[string]string{"error": "未找到个案"}, 404)
		return
	}
	rows := []map[string]interface{}{}
	for i, a := range c.Recaptures {
		if genFilter > 0 && a.Generation != genFilter {
			continue
		}
		if authFilter > 0 && a.AuthorizationVersion != authFilter {
			continue
		}
		st := strings.ToUpper(a.Status)
		if a.RevokedAt != nil {
			st = "REVOKED"
		} else if a.ConsumedAt != nil {
			st = "CONSUMED"
		} else if !a.ExpiresAt.IsZero() && asOf.After(a.ExpiresAt) {
			st = "EXPIRED"
		} else if st == "" || st == "AUTHORIZED" {
			st = "ACTIVE"
		}
		if i < len(c.Recaptures)-1 && st == "ACTIVE" {
			st = "SUPERSEDED"
		}
		if statusFilter != "" && st != statusFilter {
			continue
		}
		rows = append(rows, map[string]interface{}{"authorization": a, "status": st})
	}
	remaining := 0
	if c.CurrentEscalationRequirement != nil {
		remaining = c.CurrentEscalationRequirement.RemainingAttempts
	}
	projection := recaptureTimelineProjection{CaseID: id, Timeline: rows, Items: rows, RemainingAttempts: remaining, Escalation: c.CurrentEscalationRequirement, AuditHead: s.App.Audit.Head(id), Revision: c.Revision}
	s.recaptureTimelineMu.Lock()
	s.recaptureTimelines[cacheKey] = projection
	s.recaptureTimelineMu.Unlock()
	out(w, projection, 200)
}
