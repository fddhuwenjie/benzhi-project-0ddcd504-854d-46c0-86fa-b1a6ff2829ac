package httpapi

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/store"
	"net/http"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) (http.Handler, *store.Store, *audit.Audit) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(application.New(s, a)).Routes(), s, a
}

func TestBatchRegistrationModesAndIdempotency(t *testing.T) {
	h, s, a := newTestHandler(t)
	atomic := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "batch-atomic", "mode": "atomic", "items": []interface{}{
			map[string]interface{}{"accession_code": " dup-1 ", "title": "甲", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘"},
			map[string]interface{}{"accession_code": "DUP-1", "title": "乙", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘"},
		},
	}, http.StatusOK)
	if atomic["created_count"].(float64) != 0 || s.Count() != 0 {
		t.Fatalf("原子批次发生部分写入: %#v", atomic)
	}
	atomicResults := atomic["results"].([]interface{})
	if atomicResults[0].(map[string]interface{})["status"] != "conflict" {
		t.Fatalf("未返回批内重复位置: %#v", atomic)
	}

	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "existing", "accession_code": "EXIST-1", "title": "存量", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘",
	}, http.StatusCreated)
	partialPayload := map[string]interface{}{
		"request_id": "batch-partial", "submission_mode": "partial", "items": []interface{}{
			map[string]interface{}{"accession_code": "NEW-1", "title": "合法", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘"},
			map[string]interface{}{"accession_code": "NEW-2", "title": "", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘"},
			map[string]interface{}{"accession_code": "EXIST-1", "title": "冲突", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盘"},
		},
	}
	partial := requestJSON(t, h, http.MethodPost, "/v1/cases", partialPayload, http.StatusOK)
	results := partial["results"].([]interface{})
	if partial["created_count"].(float64) != 1 || s.Count() != 2 || results[0].(map[string]interface{})["status"] != "created" || results[1].(map[string]interface{})["status"] != "invalid" || results[2].(map[string]interface{})["existing_case_id"] != created["id"] {
		t.Fatalf("逐项批次回执异常: %#v", partial)
	}
	newID := results[0].(map[string]interface{})["case_id"].(string)
	retried := requestJSON(t, h, http.MethodPost, "/v1/cases", partialPayload, http.StatusOK)
	if retried["results"].([]interface{})[0].(map[string]interface{})["case_id"] != newID || s.Count() != 2 || len(a.Events(newID)) != 1 {
		t.Fatal("批次重试未复用逐项幂等结果")
	}
}

func TestControlledPlanCaptureQCRecaptureAndPreview(t *testing.T) {
	h, s, a := newTestHandler(t)
	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "flow-create", "accession_code": "FLOW-1", "title": "流程", "rights_note": "馆藏", "carrier_type": "stereo tape", "content_scope": "全盘",
	}, http.StatusCreated)
	id := created["id"].(string)
	path := func(action string) string { return "/v1/cases/" + id + "/" + action }
	requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{
		"request_id": "flow-assess", "expected_revision": 1, "assessor": "评估员", "mold_level": "none", "breakage": false, "adhesion": false, "contamination": false, "contamination_notes": "", "playback_risk": "low", "no_treatment_required": true, "treatment_evidence": []interface{}{},
	}, http.StatusOK)
	initial := planRequest("flow-plan-1", 2)
	initial["sample_rate_hz"] = 48000
	plan1 := requestJSON(t, h, http.MethodPost, path("plan"), initial, http.StatusOK)
	revisedPayload := planRequest("flow-plan-2", 3)
	revisedPayload["revision_reason"] = "提高保存母版采样率"
	plan2 := requestJSON(t, h, http.MethodPost, path("plan"), revisedPayload, http.StatusOK)
	if plan2["plan_revision"].(float64) != plan1["plan_revision"].(float64)+1 || plan2["state"] != "READY_FOR_CAPTURE" {
		t.Fatalf("方案修订异常: %#v", plan2)
	}
	caseAfterPlan := requestJSON(t, h, http.MethodGet, "/v1/cases/"+id, nil, http.StatusOK)
	if len(caseAfterPlan["plan_history"].([]interface{})) != 2 {
		t.Fatal("方案历史未完整保留")
	}

	base := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Second)
	oldPlanCapture := captureRequest("old-plan", 4, 1, int64(plan1["plan_revision"].(float64)), plan1["plan_fingerprint"].(string), base, "1111111111111111111111111111111111111111111111111111111111111111")
	oldConflict := requestJSON(t, h, http.MethodPost, path("captures"), oldPlanCapture, http.StatusConflict)
	if oldConflict["current_plan_revision"] != plan2["plan_revision"] {
		t.Fatalf("旧方案冲突未返回当前版本: %#v", oldConflict)
	}

	validCapture := captureRequest("capture-valid-1", 4, 1, int64(plan2["plan_revision"].(float64)), plan2["plan_fingerprint"].(string), base, "2222222222222222222222222222222222222222222222222222222222222222")
	expired := cloneMap(validCapture)
	expired["request_id"] = "capture-expired"
	expired["calibration_valid_until"] = base.Add(59 * time.Second)
	requestJSON(t, h, http.MethodPost, path("captures"), expired, http.StatusBadRequest)
	mismatch := cloneMap(validCapture)
	mismatch["request_id"] = "capture-mismatch"
	mismatch["actual_sample_rate_hz"] = 48000
	mismatchResponse := requestJSON(t, h, http.MethodPost, path("captures"), mismatch, http.StatusBadRequest)
	if len(mismatchResponse["mismatch_fields"].([]interface{})) != 1 {
		t.Fatalf("参数不符明细异常: %#v", mismatchResponse)
	}
	tooSmall := cloneMap(validCapture)
	tooSmall["request_id"] = "capture-small"
	tooSmall["asset_size_bytes"] = 1
	requestJSON(t, h, http.MethodPost, path("captures"), tooSmall, http.StatusBadRequest)
	requestJSON(t, h, http.MethodPost, path("captures"), validCapture, http.StatusOK)
	alteredCalibration := captureRequest("capture-altered-calibration", 5, 2, int64(plan2["plan_revision"].(float64)), plan2["plan_fingerprint"].(string), base.Add(time.Minute), "5555555555555555555555555555555555555555555555555555555555555555")
	alteredCalibration["calibration_reference"] = validCapture["calibration_reference"]
	alteredCalibration["calibration_valid_until"] = base.Add(2 * time.Hour)
	calibrationConflict := requestJSON(t, h, http.MethodPost, path("captures"), alteredCalibration, http.StatusConflict)
	if calibrationConflict["conflict_field"] != "calibration_reference" {
		t.Fatalf("校准引用改写未被识别: %#v", calibrationConflict)
	}
	planAfterCapture := planRequest("plan-after-capture", 5)
	planAfterCapture["revision_reason"] = "采集后不应允许修订"
	requestJSON(t, h, http.MethodPost, path("plan"), planAfterCapture, http.StatusConflict)

	badCount := qualityRequest("quality-count", 5, 1, 2)
	requestJSON(t, h, http.MethodPost, path("quality"), badCount, http.StatusBadRequest)
	outOfBounds := qualityRequest("quality-out-of-bounds", 5, 1, 1)
	outOfBounds["defect_markers"].([]interface{})[0].(map[string]interface{})["position_ms"] = 58990
	outOfBounds["defect_markers"].([]interface{})[0].(map[string]interface{})["duration_ms"] = 20
	requestJSON(t, h, http.MethodPost, path("quality"), outOfBounds, http.StatusBadRequest)
	failed := requestJSON(t, h, http.MethodPost, path("quality"), qualityRequest("quality-fail", 5, 1, 1), http.StatusOK)
	if failed["state"] != "RECAPTURE_REQUIRED" {
		t.Fatalf("质量失败状态异常: %#v", failed)
	}

	expires := time.Now().UTC().Add(10 * time.Minute)
	authorization := map[string]interface{}{"request_id": "auth-1", "expected_revision": 6, "failed_generation": 1, "authorized_by": "主管", "expires_at": expires, "remediations": []interface{}{map[string]interface{}{"category": "clipping", "action": "降低模拟增益", "owner": "采集员甲", "completion_criteria": "全程峰值保持安全余量"}}}
	requestJSON(t, h, http.MethodPost, path("recaptures"), authorization, http.StatusOK)
	expiredRecapture := captureRequest("capture-expired-auth", 7, 2, int64(plan2["plan_revision"].(float64)), plan2["plan_fingerprint"].(string), expires.Add(time.Second), "3333333333333333333333333333333333333333333333333333333333333333")
	requestJSON(t, h, http.MethodPost, path("captures"), expiredRecapture, http.StatusConflict)
	validRecapture := captureRequest("capture-valid-2", 7, 2, int64(plan2["plan_revision"].(float64)), plan2["plan_fingerprint"].(string), base.Add(3*time.Minute), "4444444444444444444444444444444444444444444444444444444444444444")
	requestJSON(t, h, http.MethodPost, path("captures"), validRecapture, http.StatusOK)
	requestJSON(t, h, http.MethodPost, path("quality"), qualityRequest("quality-pass", 8, 2, 0), http.StatusOK)

	eventsBefore := len(a.Events(id))
	preview := requestJSON(t, h, http.MethodGet, path("manifest")+"?preview=true", nil, http.StatusOK)
	afterPreview, _ := s.Get(id)
	if preview["sealable"] != true || afterPreview.Revision != 9 || len(a.Events(id)) != eventsBefore {
		t.Fatalf("预检产生了副作用: %#v", preview)
	}
	requestJSON(t, h, http.MethodPost, path("seal"), map[string]interface{}{"request_id": "seal-wrong", "expected_revision": 9, "sealed_by": "保管员", "expected_manifest_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, http.StatusConflict)
	sealed := requestJSON(t, h, http.MethodPost, path("seal"), map[string]interface{}{"request_id": "seal-right", "expected_revision": 9, "sealed_by": "保管员", "expected_manifest_digest": preview["candidate_manifest_digest"]}, http.StatusOK)
	if sealed["canonical_payload_digest"] != preview["candidate_manifest_digest"] {
		t.Fatal("正式清单摘要与预检确认值不一致")
	}
}

func cloneMap(source map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
