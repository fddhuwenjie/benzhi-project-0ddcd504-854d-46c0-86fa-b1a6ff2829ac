package httpapi

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/store"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPublicWorkflow(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := New(application.New(s, a)).Routes()

	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "create-1", "accession_code": " abc-01 ", "title": "口述史", "rights_note": "馆藏所有", "carrier_type": "stereo tape", "content_scope": "全盘",
	}, http.StatusCreated)
	id := created["id"].(string)
	if created["accession_code"] != "ABC-01" || created["first_audit_at"] == "" {
		t.Fatalf("登记规范化结果异常: %#v", created)
	}
	retried := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "create-1", "accession_code": " abc-01 ", "title": "口述史", "rights_note": "馆藏所有", "carrier_type": "stereo tape", "content_scope": "全盘",
	}, http.StatusCreated)
	if retried["id"] != id || len(a.Events(id)) != 1 || s.Count() != 1 {
		t.Fatal("登记幂等结果产生了重复快照或审计事件")
	}
	conflict := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "create-2", "accession_code": "ABC-01", "title": "重复", "rights_note": "馆藏所有", "carrier_type": "tape", "content_scope": "全盘",
	}, http.StatusConflict)
	if conflict["case_id"] != id || conflict["revision"].(float64) != 1 {
		t.Fatalf("馆藏号冲突缺少既有个案信息: %#v", conflict)
	}

	path := func(action string) string { return fmt.Sprintf("/v1/cases/%s/%s", id, action) }
	badAssessment := assessmentRequest("assess-bad", 1)
	badAssessment["treatment_evidence"] = badAssessment["treatment_evidence"].([]interface{})[:1]
	requestJSON(t, h, http.MethodPost, path("assessment"), badAssessment, http.StatusBadRequest)
	assessment := assessmentRequest("assess-ok", 1)
	assessed := requestJSON(t, h, http.MethodPost, path("assessment"), assessment, http.StatusOK)
	if assessed["state"] != "ASSESSED" || assessed["risk_summary"] == "" {
		t.Fatalf("评估结果异常: %#v", assessed)
	}

	badPlan := planRequest("plan-bad", 2)
	badPlan["approved_by"] = "采集员甲"
	requestJSON(t, h, http.MethodPost, path("plan"), badPlan, http.StatusBadRequest)
	plan := requestJSON(t, h, http.MethodPost, path("plan"), planRequest("plan-ok", 2), http.StatusOK)
	fingerprint := plan["plan_fingerprint"].(string)
	planRevision := int64(plan["plan_revision"].(float64))
	if len(fingerprint) != 64 || planRevision != 3 {
		t.Fatalf("方案指纹异常: %#v", plan)
	}

	now := time.Now().UTC().Truncate(time.Second)
	capture1 := captureRequest("capture-1", 3, 1, planRevision, fingerprint, now, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	requestJSON(t, h, http.MethodPost, path("captures"), capture1, http.StatusOK)
	quality1 := qualityRequest("quality-1", 4, 1, 1)
	failed := requestJSON(t, h, http.MethodPost, path("quality"), quality1, http.StatusOK)
	if failed["decision"] != "FAIL" || failed["state"] != "RECAPTURE_REQUIRED" {
		t.Fatalf("质量失败分类异常: %#v", failed)
	}
	queue := requestJSON(t, h, http.MethodGet, "/v1/cases?state=RECAPTURE_REQUIRED&failure_category=clipping", nil, http.StatusOK)
	if queue["total"].(float64) != 1 {
		t.Fatalf("失败分类队列异常: %#v", queue)
	}
	requestJSON(t, h, http.MethodPost, path("recaptures"), map[string]interface{}{
		"request_id": "recapture-bad", "expected_revision": 5, "failed_generation": 1, "remediations": []interface{}{}, "authorized_by": "主管甲", "expires_at": time.Now().UTC().Add(time.Hour),
	}, http.StatusBadRequest)
	requestJSON(t, h, http.MethodPost, path("recaptures"), map[string]interface{}{
		"request_id": "recapture-ok", "expected_revision": 5, "failed_generation": 1, "remediations": []interface{}{map[string]interface{}{"category": "clipping", "action": "降低增益", "owner": "采集员甲", "completion_criteria": "峰值低于负三分贝"}}, "authorized_by": "主管甲", "expires_at": time.Now().UTC().Add(time.Hour),
	}, http.StatusOK)
	requestJSON(t, h, http.MethodPost, path("seal"), map[string]interface{}{
		"request_id": "seal-early", "expected_revision": 6, "sealed_by": "保管员甲", "expected_manifest_digest": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	}, http.StatusConflict)

	capture2 := captureRequest("capture-2", 6, 2, planRevision, fingerprint, now.Add(2*time.Minute), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	requestJSON(t, h, http.MethodPost, path("captures"), capture2, http.StatusOK)
	passed := requestJSON(t, h, http.MethodPost, path("quality"), qualityRequest("quality-2", 7, 2, 0), http.StatusOK)
	if passed["decision"] != "PASS" || passed["state"] != "QC_PASSED" {
		t.Fatalf("质量通过结果异常: %#v", passed)
	}
	preview := requestJSON(t, h, http.MethodGet, path("manifest")+"?preview=true", nil, http.StatusOK)
	sealed := requestJSON(t, h, http.MethodPost, path("seal"), map[string]interface{}{
		"request_id": "seal-ok", "expected_revision": 8, "sealed_by": "保管员甲", "expected_manifest_digest": preview["candidate_manifest_digest"],
	}, http.StatusOK)
	if sealed["state"] != "SEALED" {
		t.Fatalf("封存失败: %#v", sealed)
	}
	verification := requestJSON(t, h, http.MethodGet, path("verify"), nil, http.StatusOK)
	if verification["valid"] != true {
		t.Fatalf("完整性验证失败: %#v", verification)
	}
	auditPage := requestJSON(t, h, http.MethodGet, path("audit")+"?event_type=QUALITY&limit=1", nil, http.StatusOK)
	qualityEvents := auditPage["events"].([]interface{})
	if len(qualityEvents) != 1 || qualityEvents[0].(map[string]interface{})["type"] != "QUALITY" || auditPage["has_more"] != true || auditPage["integrity_status"] != "verified" {
		t.Fatalf("质量审计筛选或游标异常: %#v", auditPage)
	}
	if auditPage["current_head_digest"] != verification["audit_head"] || auditPage["response_digest"] == "" {
		t.Fatalf("审计链头未与保存包验证对齐: %#v", auditPage)
	}
	next := int64(auditPage["next_after_revision"].(float64))
	nextPage := requestJSON(t, h, http.MethodGet, fmt.Sprintf("%s?event_type=QUALITY&limit=1&after_revision=%d", path("audit"), next), nil, http.StatusOK)
	if len(nextPage["events"].([]interface{})) != 1 || nextPage["events"].([]interface{})[0].(map[string]interface{})["revision"].(float64) <= float64(next) {
		t.Fatalf("审计原始 revision 游标未继续到下一质量事件: %#v", nextPage)
	}
	snapshotBefore, _ := os.ReadFile(dir + "/snapshot.json")
	auditBefore, _ := os.ReadFile(dir + "/audit.jsonl")
	requestJSON(t, h, http.MethodGet, path("audit")+"?from_time=2026-01-02T00:00:00Z&to_time=2026-01-01T00:00:00Z", nil, http.StatusBadRequest)
	requestJSON(t, h, http.MethodGet, path("audit")+"?event_type=UNKNOWN", nil, http.StatusBadRequest)
	requestJSON(t, h, http.MethodGet, path("audit")+"?limit=101", nil, http.StatusBadRequest)
	snapshotAfter, _ := os.ReadFile(dir + "/snapshot.json")
	auditAfter, _ := os.ReadFile(dir + "/audit.jsonl")
	if !bytes.Equal(snapshotBefore, snapshotAfter) || !bytes.Equal(auditBefore, auditAfter) {
		t.Fatal("非法审计检索产生了持久化副作用")
	}
	tamperedAudit := strings.Replace(string(auditBefore), `"type":"QUALITY"`, `"type":"CAPTURED"`, 1)
	if tamperedAudit == string(auditBefore) {
		t.Fatal("未找到可篡改的质量审计事件")
	}
	if err = os.WriteFile(dir+"/audit.jsonl", []byte(tamperedAudit), 0644); err != nil {
		t.Fatal(err)
	}
	brokenAudit := requestJSON(t, h, http.MethodGet, path("audit")+"?event_type=QUALITY", nil, http.StatusOK)
	if brokenAudit["integrity_status"] != "integrity_error" || len(brokenAudit["errors"].([]interface{})) == 0 || brokenAudit["expected_current_head_digest"] == brokenAudit["actual_current_head_digest"] {
		t.Fatalf("审计事件篡改未返回可诊断完整性结果: %#v", brokenAudit)
	}
	if err = os.WriteFile(dir+"/audit.jsonl", auditBefore, 0644); err != nil {
		t.Fatal(err)
	}
	restoredAudit := requestJSON(t, h, http.MethodGet, path("audit"), nil, http.StatusOK)
	if restoredAudit["integrity_status"] != "verified" {
		t.Fatalf("恢复审计数据后未重新验证通过: %#v", restoredAudit)
	}
	manifest := requestJSON(t, h, http.MethodGet, path("manifest"), nil, http.StatusOK)
	canonical := manifest["canonical_payload"].(map[string]interface{})
	if len(canonical["captures"].([]interface{})) != 2 || len(canonical["quality"].([]interface{})) != 2 {
		t.Fatalf("保存包未保留完整代次与质量结论: %#v", manifest)
	}
	listing := requestJSON(t, h, http.MethodGet, "/v1/cases?state=SEALED&limit=1", nil, http.StatusOK)
	if listing["total"].(float64) != 1 || listing["count"].(float64) != 1 {
		t.Fatalf("分页总数异常: %#v", listing)
	}
	inspection := requestJSON(t, h, http.MethodGet, "/v1/cases?state=SEALED&integrity_check=true&limit=1", nil, http.StatusOK)
	if inspection["integrity_results"].([]interface{})[0].(map[string]interface{})["status"] != "VALID" {
		t.Fatalf("批量巡检结果异常: %#v", inspection)
	}
	requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{}, http.StatusConflict)
	tampered, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	tampered.Manifest.CanonicalPayloadDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err = s.Put(tampered); err != nil {
		t.Fatal(err)
	}
	invalid := requestJSON(t, h, http.MethodGet, path("verify"), nil, http.StatusOK)
	if invalid["valid"] != false {
		t.Fatalf("保存包破坏未被识别: %#v", invalid)
	}
}

func TestCustodyPublicEntry(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.New(dir)
	a, _ := audit.New(dir)
	h := New(application.New(s, a)).Routes()
	now := time.Now().UTC()
	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "custody-http", "accession_code": "CUSTODY-HTTP", "title": "保管测试", "rights_note": "馆藏所有", "carrier_type": "mono tape", "content_scope": "全盘",
		"intake_receipt": map[string]interface{}{"transfer_organization": "移交单位", "transferor": "甲", "receiver": "丙", "received_at": now.Add(-3 * time.Hour), "batch_number": "batch-2", "packaging_condition": "包装完好"},
		"custody_events": []interface{}{
			map[string]interface{}{"transferor": "甲", "receiver": "乙", "occurred_at": now.Add(-2 * time.Hour), "location_code": "VAULT-A1", "seal_status": "SEALED", "notes": "完成首次交接"},
			map[string]interface{}{"transferor": "乙", "receiver": "丙", "occurred_at": now.Add(-time.Hour), "location_code": "VAULT-B2", "seal_status": "INTACT", "notes": "完成入库交接"},
		},
	}, http.StatusCreated)
	if created["current_custodian"] != "丙" || created["current_location_code"] != "VAULT-B2" || len(created["custody_chain_digest"].(string)) != 64 {
		t.Fatalf("公开入口未返回保管结论: %#v", created)
	}
	events := a.Events(created["id"].(string))
	if len(events) != 1 || events[0].EvidenceDigests["custody_chain"] == "" {
		t.Fatalf("首个审计事件未固化责任链摘要: %#v", events)
	}
}

func assessmentRequest(requestID string, revision int64) map[string]interface{} {
	completed := time.Now().UTC().Add(-time.Minute)
	return map[string]interface{}{
		"request_id": requestID, "expected_revision": revision, "assessor": "评估员甲", "mold_level": "high", "breakage": false, "adhesion": false, "contamination": true, "contamination_notes": "可见霉斑", "playback_risk": "low", "required_treatment": "隔离并清洁后低速播放", "treatment_evidence": []interface{}{map[string]interface{}{"category": "mold", "action": "隔离清洁", "performed_by": "修复员甲", "completed_at": completed, "evidence_summary": "清洁前后照片已核对"}, map[string]interface{}{"category": "contamination", "action": "去污", "performed_by": "修复员甲", "completed_at": completed, "evidence_summary": "表面去污记录已核对"}},
	}
}

func planRequest(requestID string, revision int64) map[string]interface{} {
	return map[string]interface{}{
		"request_id": requestID, "expected_revision": revision, "playback_device": "Deck-1", "signal_chain": "Deck-1>ADC-1", "target_codec": "bwf", "sample_rate_hz": 96000, "bit_depth": 24, "channel_map": "L/R", "operator": "采集员甲", "approved_by": "主管乙",
	}
}

func captureRequest(requestID string, revision int64, generation int, planRevision int64, fingerprint string, started time.Time, assetDigest string) map[string]interface{} {
	size := int64(59000 * 96000 * 24 * 2 / 8000)
	return map[string]interface{}{
		"request_id": requestID, "expected_revision": revision, "generation": generation, "calibration_reference": fmt.Sprintf("CAL-2026-%02d", generation), "calibration_device": "Deck-1", "calibrated_at": started.Add(-time.Hour), "calibration_valid_until": started.Add(time.Hour), "started_at": started, "ended_at": started.Add(time.Minute), "asset_digest": assetDigest, "asset_size_bytes": size, "container_format": "bwf", "actual_codec": "bwf", "actual_sample_rate_hz": 96000, "actual_bit_depth": 24, "actual_channels": 2, "duration_ms": 59000, "peak_dbfs": -3.2, "plan_revision": planRevision, "plan_fingerprint": fingerprint,
	}
}

func qualityRequest(requestID string, revision int64, generation, clipping int) map[string]interface{} {
	markers := []interface{}{}
	if clipping > 0 {
		markers = append(markers, map[string]interface{}{"defect_type": "clipping", "position_ms": 1000, "duration_ms": 20, "channel": "L", "description": "波形连续削顶"})
	}
	return map[string]interface{}{
		"request_id": requestID, "expected_revision": revision, "generation": generation, "completeness_passed": true, "clipping_events": clipping, "dropout_events": 0, "channel_mapping_passed": true, "duration_variance_ms": 0, "listening_notes": "已完整听检", "reviewer": "复核员甲", "defect_markers": markers,
	}
}

func requestJSON(t *testing.T, h http.Handler, method, path string, payload interface{}, want int) map[string]interface{} {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != want {
		t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
