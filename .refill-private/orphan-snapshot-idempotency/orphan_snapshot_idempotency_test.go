package orphan_snapshot_idempotency_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/httpapi"
	"archiveflow/internal/store"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOrphanSnapshotDoesNotRestoreUncommittedIdempotency(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]interface{}{
		"request_id":     "orphan-create",
		"accession_code": "ORPHAN-001",
		"title":          "临时快照中的个案",
		"rights_note":    "馆藏所有",
		"carrier_type":   "open reel",
		"content_scope":  "全盘",
	}
	first := postCase(t, httpapi.New(application.New(st, log)).Routes(), payload)
	orphanID, _ := first["id"].(string)
	if orphanID == "" {
		t.Fatalf("首次建档未返回 id: %#v", first)
	}

	pending, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	committed := []byte(`{"cases":{},"idempotency":{}}`)
	if err = os.WriteFile(filepath.Join(dir, "snapshot.json"), committed, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "snapshot.tmp"), pending, 0644); err != nil {
		t.Fatal(err)
	}

	recovered, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	restartedAudit, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(application.New(recovered, restartedAudit)).Routes()
	retried := postCase(t, handler, payload)
	retriedID, _ := retried["id"].(string)

	lookup := httptest.NewRecorder()
	handler.ServeHTTP(lookup, httptest.NewRequest(http.MethodGet, "/v1/cases/"+retriedID, nil))
	if lookup.Code != http.StatusOK {
		t.Fatalf("重试返回了未提交的幽灵个案 %q，随后查询状态为 %d", retriedID, lookup.Code)
	}
}

func postCase(t *testing.T, handler http.Handler, payload interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/cases", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("建档状态为 %d，响应为 %s", response.Code, response.Body.String())
	}
	var result map[string]interface{}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
