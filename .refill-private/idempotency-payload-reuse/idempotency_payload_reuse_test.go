package idempotency_payload_reuse_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/httpapi"
	"archiveflow/internal/store"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdempotencyKeyRejectsChangedPayload(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(application.New(s, a)).Routes()

	first := postCase(t, h, map[string]interface{}{
		"request_id":     "reused-request",
		"accession_code": "IDEM-001",
		"title":          "第一盘录音",
		"rights_note":    "馆藏所有",
		"carrier_type":   "open reel tape",
		"content_scope":  "全盘",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("首次建档状态异常: %d, body=%s", first.Code, first.Body.String())
	}
	var created domain.DigitizationCase
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("首次建档响应无法解析: %v", err)
	}

	changed := postCase(t, h, map[string]interface{}{
		"request_id":     "reused-request",
		"accession_code": "IDEM-002",
		"title":          "第二盘录音",
		"rights_note":    "委托保管",
		"carrier_type":   "compact cassette",
		"content_scope":  "A 面",
	})
	if changed.Code != http.StatusConflict {
		t.Fatalf("同一 request_id 更换载荷后应返回 409，实际为 %d, body=%s", changed.Code, changed.Body.String())
	}
	if s.Count() != 1 || len(a.Events(created.ID)) != 1 {
		t.Fatalf("载荷冲突不应创建额外个案或审计事件，实际个案数为 %d，审计事件数为 %d", s.Count(), len(a.Events(created.ID)))
	}
}

func postCase(t *testing.T, h http.Handler, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/cases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}
