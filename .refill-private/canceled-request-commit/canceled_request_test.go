package canceledrequestcommit

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/httpapi"
	"archiveflow/internal/store"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanceledRequestDoesNotCommitCase(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	au, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st, au)
	server := httpapi.New(app).Routes()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := `{"request_id":"cancel-1","accession_code":"ACC-CANCEL-1","title":"已取消请求","rights_note":"rights","carrier_type":"tape","content_scope":"scope"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/cases", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)

	if st.Count() != 0 {
		t.Fatalf("canceled request committed a case (status=%d, response=%s)", rr.Code, rr.Body.String())
	}
}
