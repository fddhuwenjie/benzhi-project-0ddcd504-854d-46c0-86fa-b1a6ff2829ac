package persistence_error_chain_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/httpapi"
	"archiveflow/internal/store"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPersistenceFailurePreservesErrorChainAndServerClassification(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "snapshot.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, appErr := application.New(s, a).CreateWithRequest(
		"direct-resource-failure", "RESOURCE-FAIL-1", "资源失效测试", "馆藏所有",
		"mono tape", "全盘",
	)
	if appErr == nil {
		t.Fatal("预期临时快照资源失效")
	}
	if !errors.Is(appErr, domain.ErrPersistence) || !errors.Is(appErr, syscall.EISDIR) {
		t.Errorf("持久化错误链丢失: %v", appErr)
	}

	payload := map[string]interface{}{
		"request_id":     "http-resource-failure",
		"accession_code": "RESOURCE-FAIL-2",
		"title":          "资源失效测试",
		"rights_note":    "馆藏所有",
		"carrier_type":   "mono tape",
		"content_scope":  "全盘",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/cases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	httpapi.New(application.New(s, a)).Routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("持久化错误应映射为 500，实际为 %d: %s", recorder.Code, recorder.Body.String())
	}
	if s.Count() != 0 {
		t.Errorf("失败建档不应写入个案，实际数量为 %d", s.Count())
	}
}
