package custody_chain_stale_cache_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"testing"
	"time"
)

func TestCustodyChainRefreshesAfterTransfer(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(s, a)
	registeredAt := time.Now().UTC().Add(-time.Hour)
	c, err := app.CreateWithCustodyRequest(
		"create-custody-cache",
		"ACC-CUSTODY-CACHE",
		"缓存失效复现载体",
		"永久保存授权",
		"open-reel",
		"口述历史录音",
		nil,
		nil,
		nil,
		[]domain.CustodyEvent{{
			Transferor:   "捐赠方",
			Receiver:     "接收员",
			OccurredAt:   registeredAt,
			LocationCode: "INGEST-01",
			SealStatus:   "INTACT",
			Notes:        "完成入库交接并确认包装完整",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	first, err := app.CustodyChain(c.ID, nil, nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.EventCount != 1 || first.CurrentCustodian != "接收员" {
		t.Fatalf("unexpected initial projection: %+v", first)
	}

	transferredAt := registeredAt.Add(30 * time.Minute)
	updated, err := app.CustodyWithRequest("transfer-custody-cache", c.ID, c.Revision, domain.CustodyEvent{
		Transferor:   "接收员",
		Receiver:     "库房管理员",
		OccurredAt:   transferredAt,
		LocationCode: "VAULT-02",
		SealStatus:   "INTACT",
		Notes:        "完成库房移交并复核包装封签状态",
	})
	if err != nil {
		t.Fatal(err)
	}

	second, err := app.CustodyChain(c.ID, nil, nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.EventCount != 2 || len(second.Events) != 2 || second.CurrentCustodian != "库房管理员" || second.CurrentLocationCode != "VAULT-02" || second.CustodyChainDigest != updated.CustodyChainDigest || second.AuditHead != a.Head(c.ID) {
		t.Fatalf("custody projection stayed stale after revision %d: %+v", updated.Revision, second)
	}
}
