package componentproofcacherace_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"runtime"
	"sync"
	"testing"
)

func TestConcurrentComponentProofCacheIsRaceFree(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })

	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.DigitizationCase{
		ID:       "case-component-proof-race",
		State:    domain.StateSealed,
		Revision: 2,
		Manifest: &domain.PreservationManifest{AuditHeadDigest: "registered-head"},
	}
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	app := application.New(s, a)

	start := make(chan struct{})
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for _, component := range []string{"registration", "assessment"} {
		component := component
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := app.ComponentProof(c.ID, component, 0)
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("ComponentProof 返回意外错误: %v", err)
		}
	}
}
