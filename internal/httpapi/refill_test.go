package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestFacetAcclimatizationTaskFixityAndMetrics(t *testing.T) {
	h, s, a := newTestHandler(t)
	bad := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "facet-bad", "accession_code": "FACET-BAD", "title": "坏分面", "rights_note": "馆藏", "carrier_type": "磁带", "content_scope": "全盒",
		"carrier_facets": []interface{}{map[string]interface{}{"facet_id": "A", "label": "A 面", "physical_order": 1, "content_scope": "上半", "playable": true}, map[string]interface{}{"facet_id": "A", "label": "B 面", "physical_order": 3, "content_scope": "下半", "playable": true}},
	}, http.StatusBadRequest)
	if bad["facet_id"] != "A" || s.Count() != 0 {
		t.Fatalf("非法分面发生落盘: %#v", bad)
	}

	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{
		"request_id": "facet-ok", "accession_code": "FACET-1", "title": "双面磁带", "rights_note": "馆藏", "carrier_type": "受潮 stereo tape", "content_scope": "全盒",
		"carrier_facets": []interface{}{map[string]interface{}{"facet_id": "B", "label": "B 面", "physical_order": 2, "content_scope": "第二段", "playable": true}, map[string]interface{}{"facet_id": "A", "label": "A 面", "physical_order": 1, "content_scope": "第一段", "playable": true}},
	}, http.StatusCreated)
	id := created["id"].(string)
	path := func(action string) string { return "/v1/cases/" + id + "/" + action }
	if created["carrier_facets"].([]interface{})[0].(map[string]interface{})["facet_id"] != "A" || created["carrier_facets_digest"] == "" {
		t.Fatalf("分面未规范化: %#v", created)
	}
	if a.Events(id)[0].EvidenceDigests["carrier_facets"] != created["carrier_facets_digest"] {
		t.Fatal("首个审计事件未锚定分面摘要")
	}

	now := time.Now().UTC().Truncate(time.Second)
	requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{
		"request_id": "accl-bad", "expected_revision": 1, "assessor": "评估员", "mold_level": "none", "breakage": false, "adhesion": false, "contamination": false, "playback_risk": "low", "no_treatment_required": true, "treatment_evidence": []interface{}{},
		"acclimatization": map[string]interface{}{"required": true, "minimum_temperature_c": 18.0, "maximum_temperature_c": 24.0, "minimum_relative_humidity": 35.0, "maximum_relative_humidity": 55.0, "minimum_stable_duration_minutes": 30, "readings": []interface{}{map[string]interface{}{"measured_at": now.Add(-40 * time.Minute), "temperature_c": 20.0, "relative_humidity": 45.0, "measured_by": "评估员", "instrument_id": "ENV-1"}, map[string]interface{}{"measured_at": now.Add(-5 * time.Minute), "temperature_c": 20.0, "relative_humidity": 70.0, "measured_by": "评估员", "instrument_id": "ENV-1"}}},
	}, http.StatusBadRequest)
	assessed := requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{
		"request_id": "accl-ok", "expected_revision": 1, "assessor": "评估员", "mold_level": "none", "breakage": false, "adhesion": false, "contamination": false, "playback_risk": "low", "no_treatment_required": true, "treatment_evidence": []interface{}{},
		"acclimatization": map[string]interface{}{"required": true, "minimum_temperature_c": 18.0, "maximum_temperature_c": 24.0, "minimum_relative_humidity": 35.0, "maximum_relative_humidity": 55.0, "minimum_stable_duration_minutes": 30, "readings": []interface{}{map[string]interface{}{"measured_at": now.Add(-40 * time.Minute), "temperature_c": 20.0, "relative_humidity": 45.0, "measured_by": "评估员", "instrument_id": "ENV-1"}, map[string]interface{}{"measured_at": now.Add(-5 * time.Minute), "temperature_c": 21.0, "relative_humidity": 46.0, "measured_by": "评估员", "instrument_id": "ENV-1"}}},
	}, http.StatusOK)
	if assessed["acclimatization"].(map[string]interface{})["release_decision"] != "RELEASED" {
		t.Fatalf("稳定化未放行: %#v", assessed)
	}

	planPayload := planRequest("tasks-ok", 2)
	planPayload["capture_tasks"] = []interface{}{map[string]interface{}{"task_id": "TASK-A", "facet_id": "A", "execution_order": 1, "estimated_duration_ms": 30000, "content_start": "开头", "content_end": "中点", "channel_map": "L/R"}, map[string]interface{}{"task_id": "TASK-B", "facet_id": "B", "execution_order": 2, "estimated_duration_ms": 29000, "content_start": "中点", "content_end": "结尾", "channel_map": "L/R"}}
	planPayload["skipped_facets"] = []interface{}{}
	missingTask := cloneMap(planPayload)
	missingTask["request_id"] = "tasks-missing"
	missingTask["capture_tasks"] = planPayload["capture_tasks"].([]interface{})[:1]
	requestJSON(t, h, http.MethodPost, path("plan"), missingTask, http.StatusBadRequest)
	plan := requestJSON(t, h, http.MethodPost, path("plan"), planPayload, http.StatusOK)
	if plan["estimated_total_duration_ms"].(float64) != 59000 {
		t.Fatalf("预计总时长错误: %#v", plan)
	}

	d1 := sha256.Sum256([]byte("chunk-1"))
	d2 := sha256.Sum256([]byte("chunk-2"))
	combined := sha256.New()
	combined.Write(d1[:])
	combined.Write(d2[:])
	asset := hex.EncodeToString(combined.Sum(nil))
	capture := captureRequest("fixity-ok", 3, 1, int64(plan["plan_revision"].(float64)), plan["plan_fingerprint"].(string), now.Add(time.Minute), asset)
	size := capture["asset_size_bytes"].(int64)
	capture["capture_task_id"] = "TASK-A"
	capture["fixity_chunks"] = map[string]interface{}{"algorithm": "sha256", "chunk_size_bytes": int64(20000000), "asset_size_bytes": size, "chunks": []interface{}{map[string]interface{}{"index": 0, "size_bytes": int64(20000000), "digest": fmt.Sprintf("%x", d1)}, map[string]interface{}{"index": 1, "size_bytes": size - 20000000, "digest": fmt.Sprintf("%x", d2)}}}
	captured := requestJSON(t, h, http.MethodPost, path("captures"), capture, http.StatusOK)
	if captured["fixity_digest"] != asset || captured["capture_task_id"] != "TASK-A" {
		t.Fatalf("分块固化未保存: %#v", captured)
	}

	quality := qualityRequest("metrics-ok", 4, 1, 0)
	quality["decision"] = "PASS"
	quality["measurement_profile"] = map[string]interface{}{"tool": "AudioMeter", "tool_version": "1.2.0", "parameters_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	quality["channel_metrics"] = []interface{}{map[string]interface{}{"channel": "R", "dc_offset": 0.001, "integrated_loudness_lufs": -20.0, "noise_floor_dbfs": -45.0, "silence_ratio": 0.1}, map[string]interface{}{"channel": "L", "dc_offset": -0.001, "integrated_loudness_lufs": -21.0, "noise_floor_dbfs": -46.0, "silence_ratio": 0.12}}
	mismatch := cloneMap(quality)
	mismatch["request_id"] = "metrics-mismatch"
	mismatchMetrics := []interface{}{map[string]interface{}{"channel": "R", "dc_offset": 0.001, "integrated_loudness_lufs": -20.0, "noise_floor_dbfs": -45.0, "silence_ratio": 0.1}, map[string]interface{}{"channel": "L", "dc_offset": 0.2, "integrated_loudness_lufs": -21.0, "noise_floor_dbfs": -46.0, "silence_ratio": 0.12}}
	mismatch["channel_metrics"] = mismatchMetrics
	requestJSON(t, h, http.MethodPost, path("quality"), mismatch, http.StatusBadRequest)
	passed := requestJSON(t, h, http.MethodPost, path("quality"), quality, http.StatusOK)
	if passed["state"] != "QC_PASSED" || passed["threshold_version"] == "" || len(passed["metric_results"].([]interface{})) != 8 {
		t.Fatalf("量化质量判定异常: %#v", passed)
	}
	preview := requestJSON(t, h, http.MethodGet, path("manifest")+"?preview=true", nil, http.StatusOK)
	index := preview["candidate_manifest"].(map[string]interface{})["generation_evidence_index"].([]interface{})
	if len(index) != 1 || index[0].(map[string]interface{})["capture_task_id"] != "TASK-A" {
		t.Fatalf("代次证据索引异常: %#v", preview)
	}
}

func TestHighRiskQualityCountersign(t *testing.T) {
	h, _, _ := newTestHandler(t)
	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{"request_id": "cs-create", "accession_code": "CS-1", "title": "高风险", "rights_note": "馆藏", "carrier_type": "stereo tape", "content_scope": "全盘"}, http.StatusCreated)
	id := created["id"].(string)
	path := func(a string) string { return "/v1/cases/" + id + "/" + a }
	completed := time.Now().UTC().Add(-time.Minute)
	requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{"request_id": "cs-assess", "expected_revision": 1, "assessor": "评估员", "mold_level": "none", "breakage": false, "adhesion": false, "contamination": false, "playback_risk": "high", "treatment_evidence": []interface{}{map[string]interface{}{"category": "playback", "action": "低速播放", "performed_by": "修复员", "completed_at": completed, "evidence_summary": "已完成低速播放准备"}}}, http.StatusOK)
	plan := requestJSON(t, h, http.MethodPost, path("plan"), planRequest("cs-plan", 2), http.StatusOK)
	now := time.Now().UTC().Truncate(time.Second)
	requestJSON(t, h, http.MethodPost, path("captures"), captureRequest("cs-cap", 3, 1, int64(plan["plan_revision"].(float64)), plan["plan_fingerprint"].(string), now, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), http.StatusOK)
	primary := qualityRequest("cs-primary", 4, 1, 0)
	primary["decision"] = "PASS"
	pending := requestJSON(t, h, http.MethodPost, path("quality"), primary, http.StatusOK)
	if pending["state"] != "CAPTURED" || pending["countersign_status"] != "PENDING" {
		t.Fatalf("高风险首审未进入待会签: %#v", pending)
	}
	requestJSON(t, h, http.MethodPost, path("quality"), map[string]interface{}{"request_id": "cs-same", "expected_revision": 5, "generation": 1, "reviewer": "复核员甲", "listening_notes": "独立复核确认", "decision": "PASS", "countersign_for_revision": pending["quality_revision"], "confirmed_evidence_digest": pending["quality_evidence_digest"]}, http.StatusConflict)
	confirmed := requestJSON(t, h, http.MethodPost, path("quality"), map[string]interface{}{"request_id": "cs-ok", "expected_revision": 5, "generation": 1, "reviewer": "复核员乙", "listening_notes": "独立复核确认", "decision": "PASS", "countersign_for_revision": pending["quality_revision"], "confirmed_evidence_digest": pending["quality_evidence_digest"]}, http.StatusOK)
	if confirmed["state"] != "QC_PASSED" || confirmed["countersign_status"] != "CONFIRMED" {
		t.Fatalf("会签未落定: %#v", confirmed)
	}
}

func TestRecaptureAuthorizationRevokeRenew(t *testing.T) {
	h, _, a := newTestHandler(t)
	created := requestJSON(t, h, http.MethodPost, "/v1/cases", map[string]interface{}{"request_id": "life-create", "accession_code": "LIFE-1", "title": "授权生命周期", "rights_note": "馆藏", "carrier_type": "stereo tape", "content_scope": "全盘"}, http.StatusCreated)
	id := created["id"].(string)
	path := func(action string) string { return "/v1/cases/" + id + "/" + action }
	requestJSON(t, h, http.MethodPost, path("assessment"), map[string]interface{}{"request_id": "life-assess", "expected_revision": 1, "assessor": "评估员", "mold_level": "none", "breakage": false, "adhesion": false, "contamination": false, "playback_risk": "low", "no_treatment_required": true, "treatment_evidence": []interface{}{}}, http.StatusOK)
	plan := requestJSON(t, h, http.MethodPost, path("plan"), planRequest("life-plan", 2), http.StatusOK)
	now := time.Now().UTC().Truncate(time.Second)
	requestJSON(t, h, http.MethodPost, path("captures"), captureRequest("life-cap-1", 3, 1, int64(plan["plan_revision"].(float64)), plan["plan_fingerprint"].(string), now, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), http.StatusOK)
	requestJSON(t, h, http.MethodPost, path("quality"), qualityRequest("life-q-1", 4, 1, 1), http.StatusOK)
	remediations := []interface{}{map[string]interface{}{"category": "clipping", "action": "降低增益", "owner": "采集员甲", "completion_criteria": "峰值保持安全余量"}}
	requestJSON(t, h, http.MethodPost, path("recaptures"), map[string]interface{}{"request_id": "life-auth", "expected_revision": 5, "action": "authorize", "failed_generation": 1, "authorized_by": "主管甲", "expires_at": now.Add(time.Hour), "remediations": remediations}, http.StatusOK)
	revoked := requestJSON(t, h, http.MethodPost, path("recaptures"), map[string]interface{}{"request_id": "life-revoke", "expected_revision": 6, "action": "revoke", "authorization_version": 1, "revoked_by": "主管乙", "revocation_reason": "操作人员发生变更"}, http.StatusOK)
	if revoked["state"] != "RECAPTURE_REQUIRED" {
		t.Fatalf("撤销状态错误: %#v", revoked)
	}
	renewed := requestJSON(t, h, http.MethodPost, path("recaptures"), map[string]interface{}{"request_id": "life-renew", "expected_revision": 7, "action": "renew", "failed_generation": 1, "authorized_by": "主管丙", "expires_at": now.Add(2 * time.Hour), "remediations": remediations}, http.StatusOK)
	recaptures := renewed["recaptures"].([]interface{})
	latest := recaptures[len(recaptures)-1].(map[string]interface{})
	if latest["generation"].(float64) != 2 || latest["authorization_version"].(float64) != 2 {
		t.Fatalf("续期版本错误: %#v", renewed)
	}
	oldVersion := captureRequest("life-old-version", 8, 2, int64(plan["plan_revision"].(float64)), plan["plan_fingerprint"].(string), now.Add(10*time.Minute), "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	oldVersion["recapture_authorization_version"] = 1
	requestJSON(t, h, http.MethodPost, path("captures"), oldVersion, http.StatusConflict)
	valid := captureRequest("life-new-version", 8, 2, int64(plan["plan_revision"].(float64)), plan["plan_fingerprint"].(string), now.Add(10*time.Minute), "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	valid["recapture_authorization_version"] = 2
	requestJSON(t, h, http.MethodPost, path("captures"), valid, http.StatusOK)
	if !a.Validate(id, 9) {
		t.Fatal("授权生命周期审计链不连续")
	}
	requestJSON(t, h, http.MethodPost, path("quality"), qualityRequest("life-q-2", 9, 2, 0), http.StatusOK)
	preview := requestJSON(t, h, http.MethodGet, path("manifest")+"?preview=true", nil, http.StatusOK)
	if preview["sealable"] != true {
		t.Fatalf("重采证据关系阻断封存: %#v", preview)
	}
	requestJSON(t, h, http.MethodPost, path("seal"), map[string]interface{}{"request_id": "life-seal", "expected_revision": 10, "sealed_by": "保管员", "expected_manifest_digest": preview["candidate_manifest_digest"]}, http.StatusOK)
	manifest := requestJSON(t, h, http.MethodGet, path("manifest")+"?generation=2", nil, http.StatusOK)
	index := manifest["generation_evidence_index"].([]interface{})
	relation := index[0].(map[string]interface{})
	if len(index) != 1 || relation["recapture_authorization_version"].(float64) != 2 || relation["failed_quality_revision"].(float64) != 5 {
		t.Fatalf("重采代次证据路径错误: %#v", manifest)
	}
}
