package httpapi

import (
	"archiveflow/internal/domain"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAuditSearch(w http.ResponseWriter, r *http.Request, id string) {
	allowed := map[string]bool{"from_time": true, "to_time": true, "event_type": true, "after_revision": true, "limit": true}
	keys := make([]string, 0, len(r.URL.Query()))
	for key := range r.URL.Query() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := r.URL.Query()[key]
		if !allowed[key] {
			out(w, map[string]interface{}{"error": "未知审计查询参数", "parameter": key}, http.StatusBadRequest)
			return
		}
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			out(w, map[string]interface{}{"error": "审计查询参数必须且只能提供一个非空值", "parameter": key}, http.StatusBadRequest)
			return
		}
	}
	query := domain.AuditQuery{Limit: 50}
	var err error
	if value := r.URL.Query().Get("from_time"); value != "" {
		parsed, parseError := time.Parse(time.RFC3339Nano, value)
		if parseError != nil {
			out(w, map[string]string{"error": "from_time 无效"}, http.StatusBadRequest)
			return
		}
		query.FromTime = &parsed
	}
	if value := r.URL.Query().Get("to_time"); value != "" {
		parsed, parseError := time.Parse(time.RFC3339Nano, value)
		if parseError != nil {
			out(w, map[string]string{"error": "to_time 无效"}, http.StatusBadRequest)
			return
		}
		query.ToTime = &parsed
	}
	query.EventType = r.URL.Query().Get("event_type")
	if value := r.URL.Query().Get("after_revision"); value != "" {
		query.AfterRevision, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			out(w, map[string]string{"error": "after_revision 无效"}, http.StatusBadRequest)
			return
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		query.Limit, err = strconv.Atoi(value)
		if err != nil {
			out(w, map[string]string{"error": "limit 无效"}, http.StatusBadRequest)
			return
		}
	}
	page, err := s.App.AuditSearch(id, query)
	if err != nil {
		parseErr(w, err)
		return
	}
	out(w, page, http.StatusOK)
}

func (s *Server) handleReservationRelease(w http.ResponseWriter, r *http.Request, id string) {
	if !jsonContentType(r) {
		out(w, map[string]string{"error": "Content-Type 必须为 application/json"}, 415)
		return
	}
	var raw map[string]json.RawMessage
	if dec(r, &raw) != nil {
		out(w, map[string]string{"error": "请求体无效"}, 400)
		return
	}
	if unknown(raw, map[string]bool{"request_id": true, "expected_revision": true, "action": true, "released_by": true, "release_reason": true}) {
		out(w, map[string]string{"error": "存在未知字段"}, 400)
		return
	}
	req := requestID(r, raw)
	if req == "" {
		out(w, map[string]string{"error": "request_id 为必填项"}, 400)
		return
	}
	for _, field := range []string{"expected_revision", "action", "released_by", "release_reason"} {
		if _, ok := raw[field]; !ok {
			out(w, map[string]string{"error": field + " 为必填项"}, 400)
			return
		}
	}
	var rev int64
	var action, releasedBy, reason string
	if json.Unmarshal(raw["expected_revision"], &rev) != nil || rev <= 0 {
		out(w, map[string]string{"error": "expected_revision 无效"}, 400)
		return
	}
	if json.Unmarshal(raw["action"], &action) != nil || strings.ToUpper(strings.TrimSpace(action)) != "RELEASE" {
		out(w, map[string]string{"error": "仅支持 RELEASE 动作"}, 400)
		return
	}
	if json.Unmarshal(raw["released_by"], &releasedBy) != nil || json.Unmarshal(raw["release_reason"], &reason) != nil {
		out(w, map[string]string{"error": "释放字段无效"}, 400)
		return
	}
	c, err := s.App.ReleaseReservationWithRequest(req, id, rev, releasedBy, reason)
	if err != nil {
		parseErr(w, err)
		return
	}
	extra := map[string]interface{}{"reservation_status": c.Plan.ReservationStatus, "reservation_released_at": c.Plan.ReservationReleasedAt, "reservation_released_by": c.Plan.ReservationReleasedBy, "reservation_release_reason": c.Plan.ReservationReleaseReason, "plan_revision": c.Plan.PlanRevision, "plan_fingerprint": c.Plan.Fingerprint}
	outCase(w, c, extra, 200)
}

func (s *Server) handlePreflight(w http.ResponseWriter, requestID string, raw map[string]json.RawMessage) {
	if unknown(raw, map[string]bool{"preflight": true, "request_id": true, "mode": true, "submission_mode": true, "items": true, "accession_code": true, "title": true, "rights_note": true, "carrier_type": true, "content_scope": true, "intake_receipt": true, "carrier_facets": true, "alternative_identifiers": true, "custody_events": true}) {
		out(w, map[string]string{"error": "存在未知字段"}, 400)
		return
	}
	var items []domain.RegistrationItem
	if b, ok := raw["items"]; ok {
		var fields []map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil || len(fields) == 0 {
			out(w, map[string]string{"error": "items 无效"}, 400)
			return
		}
		items = make([]domain.RegistrationItem, len(fields))
		allowed := map[string]bool{"accession_code": true, "title": true, "rights_note": true, "carrier_type": true, "content_scope": true, "intake_receipt": true, "carrier_facets": true, "alternative_identifiers": true, "custody_events": true}
		for i, f := range fields {
			if unknown(f, allowed) {
				out(w, map[string]interface{}{"error": "登记项目存在未知字段", "index": i}, 400)
				return
			}
			if json.Unmarshal(rawBytes(f), &items[i]) != nil {
				out(w, map[string]interface{}{"error": "登记项目无效", "index": i}, 400)
				return
			}
		}
	} else {
		var x domain.RegistrationItem
		allowed := map[string]bool{"preflight": true, "request_id": true, "mode": true, "submission_mode": true, "accession_code": true, "title": true, "rights_note": true, "carrier_type": true, "content_scope": true, "intake_receipt": true, "carrier_facets": true, "alternative_identifiers": true, "custody_events": true}
		if unknown(raw, allowed) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		if json.Unmarshal(rawBytes(raw), &x) != nil {
			out(w, map[string]string{"error": "请求体无效"}, 400)
			return
		}
		items = []domain.RegistrationItem{x}
	}
	report, err := s.App.Preflight(requestID, items)
	if err != nil {
		parseErr(w, err)
		return
	}
	out(w, report, 200)
}

func (s *Server) handleCapturePreflight(w http.ResponseWriter, requestID, id string, rev int64, raw map[string]json.RawMessage) {
	allowed := captureItemFields()
	allowed["request_id"], allowed["expected_revision"], allowed["case_id"], allowed["preflight"], allowed["items"], allowed["generation"], allowed["plan_revision"], allowed["plan_fingerprint"], allowed["recapture_authorization_version"] = true, true, true, true, true, true, true, true, true
	if unknown(raw, allowed) {
		out(w, map[string]string{"error": "存在未知字段"}, 400)
		return
	}
	var items []domain.CaptureGeneration
	if value, grouped := raw["items"]; grouped {
		var entries []map[string]json.RawMessage
		if json.Unmarshal(value, &entries) != nil || len(entries) == 0 {
			out(w, map[string]string{"error": "items 无效"}, 400)
			return
		}
		items = make([]domain.CaptureGeneration, len(entries))
		for i, fields := range entries {
			normalizeArrayAliases(fields, "file_segments", fileSegmentAliases())
			if !expandFixityObject(fields) || unknown(fields, captureItemFields()) {
				out(w, map[string]interface{}{"error": "采集项目存在未知字段", "item_index": i}, 400)
				return
			}
			if field, ok := validateCaptureEvidenceFields(fields); !ok {
				out(w, map[string]interface{}{"error": "采集项目证据无效", "item_index": i, "field": field}, 400)
				return
			}
			for _, shared := range []string{"generation", "plan_revision", "plan_fingerprint", "recapture_authorization_version"} {
				if _, exists := fields[shared]; !exists {
					if v, ok := raw[shared]; ok {
						fields[shared] = v
					}
				}
			}
			if index, field, ok := validateObjectArray(fields, "calibration_measurements", calibrationMeasurementFields()); !ok {
				out(w, map[string]interface{}{"error": "calibration_measurements 无效", "item_index": i, "measurement_index": index, "field": field}, 400)
				return
			}
			if json.Unmarshal(rawBytes(fields), &items[i]) != nil {
				out(w, map[string]interface{}{"error": "采集项目无效", "item_index": i}, 400)
				return
			}
		}
	} else {
		if !expandFixityObject(raw) {
			out(w, map[string]string{"error": "fixity_chunks 无效"}, 400)
			return
		}
		normalizeArrayAliases(raw, "file_segments", fileSegmentAliases())
		if unknown(raw, allowed) {
			out(w, map[string]string{"error": "存在未知字段"}, 400)
			return
		}
		if field, ok := validateCaptureEvidenceFields(raw); !ok {
			out(w, map[string]string{"error": "采集项目证据无效", "field": field}, 400)
			return
		}
		if _, ok := raw["plan_revision"]; !ok {
			out(w, map[string]string{"error": "plan_revision 为必填项"}, 400)
			return
		}
		var item domain.CaptureGeneration
		if json.Unmarshal(rawBytes(raw), &item) != nil {
			out(w, map[string]string{"error": "采集项目无效"}, 400)
			return
		}
		items = []domain.CaptureGeneration{item}
	}
	report, err := s.App.CapturePreflight(requestID, id, rev, items)
	if err != nil {
		parseErr(w, err)
		return
	}
	out(w, report, 200)
}

func (s *Server) handleRegistrationBatch(w http.ResponseWriter, requestID string, raw map[string]json.RawMessage) {
	allowed := map[string]bool{"request_id": true, "mode": true, "submission_mode": true, "items": true}
	if unknown(raw, allowed) {
		out(w, map[string]string{"error": "存在未知字段"}, http.StatusBadRequest)
		return
	}
	var mode string
	if value, ok := raw["mode"]; ok {
		_ = json.Unmarshal(value, &mode)
	}
	if value, ok := raw["submission_mode"]; ok {
		if mode != "" {
			out(w, map[string]string{"error": "mode 与 submission_mode 不能同时提交"}, http.StatusBadRequest)
			return
		}
		_ = json.Unmarshal(value, &mode)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	var itemRaw []map[string]json.RawMessage
	if json.Unmarshal(raw["items"], &itemRaw) != nil || len(itemRaw) == 0 || len(itemRaw) > 100 {
		out(w, map[string]interface{}{"error": "items 数量无效", "minimum": 1, "maximum": 100}, http.StatusBadRequest)
		return
	}
	items := make([]domain.RegistrationItem, len(itemRaw))
	itemAllowed := map[string]bool{"accession_code": true, "title": true, "rights_note": true, "carrier_type": true, "content_scope": true, "intake_receipt": true, "carrier_facets": true, "alternative_identifiers": true, "custody_events": true}
	for index, fields := range itemRaw {
		normalizeArrayAliases(fields, "carrier_facets", map[string]string{"label_text": "label", "expected_content_scope": "content_scope"})
		normalizeArrayAliases(fields, "custody_events", custodyAliases())
		if unknown(fields, itemAllowed) {
			out(w, map[string]interface{}{"error": "登记项目存在未知字段", "index": index}, http.StatusBadRequest)
			return
		}
		if json.Unmarshal(rawBytes(fields), &items[index]) != nil {
			out(w, map[string]interface{}{"error": "登记项目无效", "index": index}, http.StatusBadRequest)
			return
		}
		if receiptRaw, exists := fields["intake_receipt"]; exists {
			var receiptFields map[string]json.RawMessage
			if json.Unmarshal(receiptRaw, &receiptFields) != nil || unknown(receiptFields, intakeReceiptFields()) {
				out(w, map[string]interface{}{"error": "intake_receipt 无效", "index": index}, http.StatusBadRequest)
				return
			}
		}
		if index, field, ok := validateObjectArray(fields, "carrier_facets", map[string]bool{"facet_id": true, "label": true, "physical_order": true, "content_scope": true, "playable": true}); !ok {
			out(w, map[string]interface{}{"error": "carrier_facets 无效", "index": index, "field": field}, http.StatusBadRequest)
			return
		}
		if itemIndex, field, ok := validateObjectArray(fields, "alternative_identifiers", map[string]bool{"type": true, "value": true}); !ok {
			out(w, map[string]interface{}{"error": "alternative_identifiers 无效", "index": index, "item_index": itemIndex, "field": field}, http.StatusBadRequest)
			return
		}
		if itemIndex, field, ok := validateObjectArray(fields, "custody_events", custodyEventFields()); !ok {
			out(w, map[string]interface{}{"error": "custody_events 无效", "index": index, "item_index": itemIndex, "field": field}, http.StatusBadRequest)
			return
		}
	}
	result, err := s.App.CreateBatch(requestID, mode, items)
	if err != nil {
		parseErr(w, err)
		return
	}
	out(w, result, http.StatusOK)
}

func intakeReceiptFields() map[string]bool {
	return map[string]bool{"transfer_organization": true, "transferor": true, "receiver": true, "received_at": true, "batch_number": true, "packaging_condition": true}
}

func validateObjectArray(raw map[string]json.RawMessage, name string, allowed map[string]bool) (int, string, bool) {
	value, exists := raw[name]
	if !exists {
		return 0, "", true
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(value, &entries) != nil {
		return 0, "", false
	}
	for index, entry := range entries {
		for field := range entry {
			if !allowed[field] {
				return index, field, false
			}
		}
	}
	return 0, "", true
}
