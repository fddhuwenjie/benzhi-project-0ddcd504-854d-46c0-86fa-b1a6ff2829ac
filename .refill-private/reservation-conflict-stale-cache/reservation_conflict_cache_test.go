package reservation_conflict_cache_test

import (
	"archiveflow/internal/application"
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"archiveflow/internal/store"
	"testing"
	"time"
)

func TestReleasedReservationInvalidatesConflictCache(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := audit.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	reserved, err := domain.NewCase("case-reserved", "RESERVED-001", "预约载体", "馆藏使用", "mono tape", "口述史")
	if err != nil {
		t.Fatal(err)
	}
	reserved.State = domain.StateReady
	reserved.Revision = 3
	reserved.Plan = &domain.CapturePlan{
		CaseID: "case-reserved", PlaybackDevice: "deck-a", Operator: "operator-a",
		PlanRevision: 3, Fingerprint: "plan-fingerprint", ValidUntil: now.Add(24 * time.Hour),
		ScheduledStart: now.Add(time.Hour), ScheduledEnd: now.Add(3 * time.Hour), ReservationStatus: "ACTIVE",
	}
	if err := s.Create(reserved); err != nil {
		t.Fatal(err)
	}
	for revision, eventType := range []string{"REGISTERED", "ASSESSED", "PLAN_APPROVED"} {
		if err := a.AppendAt(reserved.ID, eventType, int64(revision+1), now.Add(time.Duration(revision)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	proposed := domain.CapturePlan{
		PlaybackDevice: "deck-a", Operator: "operator-b",
		ScheduledStart: now.Add(90 * time.Minute), ScheduledEnd: now.Add(2 * time.Hour),
	}
	if conflicts := s.PlanResourceConflicts("case-candidate", proposed); len(conflicts) != 1 || conflicts[0].CaseID != reserved.ID {
		t.Fatalf("首次检查应发现预约冲突，实际为 %#v", conflicts)
	}

	app := application.New(s, a)
	if _, err := app.ReleaseReservationWithRequest("release-reservation", reserved.ID, 3, "调度主管", "释放设备给其他采集任务"); err != nil {
		t.Fatal(err)
	}
	if current, err := s.Get(reserved.ID); err != nil || current.Plan.ReservationStatus != "RELEASED" {
		t.Fatalf("预约释放未持久化: case=%#v err=%v", current, err)
	}

	if conflicts := s.PlanResourceConflicts("case-candidate", proposed); len(conflicts) != 0 {
		t.Fatalf("预约释放后仍返回陈旧冲突: %#v", conflicts)
	}
}
