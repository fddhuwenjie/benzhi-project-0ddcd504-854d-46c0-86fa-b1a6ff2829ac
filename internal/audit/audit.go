package audit

import (
	"archiveflow/internal/domain"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Audit struct {
	path   string
	mu     sync.Mutex
	events map[string][]domain.Event
	heads  map[string]string
}

// PersistenceReadError 为审计日志读取故障补充操作上下文。
// CauseText 用于生成稳定的 API 诊断文本。
type PersistenceReadError struct {
	Operation string
	Path      string
	CauseText string
}

func (e *PersistenceReadError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.Operation, e.Path, e.CauseText)
}

func persistenceReadError(operation, path string, cause error) error {
	return &PersistenceReadError{Operation: operation, Path: path, CauseText: cause.Error()}
}

func New(dir string) (*Audit, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	a := &Audit{path: filepath.Join(dir, "audit.jsonl"), events: map[string][]domain.Event{}, heads: map[string]string{}}
	if f, e := os.Open(a.path); e == nil {
		defer f.Close()
		dec := json.NewDecoder(f)
		for {
			var ev domain.Event
			if dec.Decode(&ev) != nil {
				break
			}
			b, _ := json.Marshal(ev)
			h := sha256.Sum256(append([]byte(a.heads[ev.CaseID]), b...))
			a.heads[ev.CaseID] = hex.EncodeToString(h[:])
			a.events[ev.CaseID] = append(a.events[ev.CaseID], ev)
		}
	}
	return a, nil
}
func (a *Audit) Append(id, typ string, rev int64) error {
	return a.AppendEvidenceAt(id, typ, rev, "", time.Now().UTC())
}
func (a *Audit) AppendAt(id, typ string, rev int64, at time.Time) error {
	return a.AppendEvidenceAt(id, typ, rev, "", at)
}
func (a *Audit) AppendEvidence(id, typ string, rev int64, evidenceDigest string) error {
	return a.AppendEvidenceAt(id, typ, rev, evidenceDigest, time.Now().UTC())
}
func (a *Audit) AppendEvidenceAt(id, typ string, rev int64, evidenceDigest string, at time.Time) error {
	return a.AppendEvidenceDigestsAt(id, typ, rev, evidenceDigest, nil, at)
}
func (a *Audit) AppendEvidenceDigests(id, typ string, rev int64, evidenceDigest string, evidenceDigests map[string]string) error {
	return a.AppendEvidenceDigestsAt(id, typ, rev, evidenceDigest, evidenceDigests, time.Now().UTC())
}
func (a *Audit) AppendEvidenceDigestsAt(id, typ string, rev int64, evidenceDigest string, evidenceDigests map[string]string, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := domain.Event{CaseID: id, Type: typ, Revision: rev, At: at.UTC(), EvidenceDigest: evidenceDigest, EvidenceDigests: evidenceDigests}
	b, _ := json.Marshal(e)
	f, er := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if er != nil {
		return er
	}
	if _, er = f.Write(append(b, '\n')); er != nil {
		_ = f.Close()
		return er
	}
	if er = f.Close(); er != nil {
		return er
	}
	h := sha256.Sum256(append([]byte(a.heads[id]), b...))
	a.heads[id] = hex.EncodeToString(h[:])
	a.events[id] = append(a.events[id], e)
	return nil
}
func (a *Audit) FirstAt(id string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events[id]) == 0 {
		return time.Time{}
	}
	return a.events[id][0].At
}
func (a *Audit) Events(id string) []domain.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]domain.Event(nil), a.events[id]...)
}
func (a *Audit) Head(id string) string { a.mu.Lock(); defer a.mu.Unlock(); return a.heads[id] }
func (a *Audit) Validate(id string, revision int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := a.events[id]
	if int64(len(events)) != revision {
		return false
	}
	previous := ""
	for i, event := range events {
		if event.CaseID != id || event.Revision != int64(i+1) {
			return false
		}
		if !validTransition(events, i) {
			return false
		}
		b, _ := json.Marshal(event)
		h := sha256.Sum256(append([]byte(previous), b...))
		previous = hex.EncodeToString(h[:])
	}
	return previous == a.heads[id]
}

func validTransition(events []domain.Event, index int) bool {
	if index == 0 {
		return events[index].Type == "REGISTERED"
	}
	current := events[index].Type
	previous := events[index-1].Type
	if current == "CUSTODY_TRANSFER" {
		return previous != "SEALED"
	}
	for previous == "CUSTODY_TRANSFER" && index > 1 {
		index--
		previous = events[index-1].Type
	}
	switch previous {
	case "REGISTERED":
		return current == "ASSESSED"
	case "ASSESSED":
		return current == "ASSESSMENT_CORRECTED" || current == "PLAN_APPROVED"
	case "ASSESSMENT_CORRECTED":
		return current == "ASSESSMENT_CORRECTED" || current == "PLAN_APPROVED"
	case "PLAN_APPROVED", "PLAN_REVISED", "PLAN_REAPPROVED":
		return current == "PLAN_REVISED" || current == "PLAN_REAPPROVED" || current == "CAPTURED" || current == "PLAN_RESERVATION_RELEASED"
	case "PLAN_RESERVATION_RELEASED":
		return current == "PLAN_REVISED" || current == "PLAN_REAPPROVED"
	case "RECAPTURE_AUTHORIZED":
		return current == "CAPTURED" || current == "RECAPTURE_REVOKED" || current == "RECAPTURE_RENEWED"
	case "RECAPTURE_REVOKED":
		return current == "RECAPTURE_RENEWED"
	case "RECAPTURE_RENEWED":
		return current == "CAPTURED" || current == "RECAPTURE_REVOKED" || current == "RECAPTURE_RENEWED"
	case "CAPTURED":
		return current == "QUALITY"
	case "QUALITY":
		return current == "QUALITY" || current == "QUALITY_ADJUDICATED" || current == "RECAPTURE_AUTHORIZED" || current == "SEALED"
	case "QUALITY_ADJUDICATED":
		return current == "RECAPTURE_AUTHORIZED" || current == "SEALED"
	default:
		return false
	}
}
func (a *Audit) HeadAt(id string, count int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if count < 0 || count > len(a.events[id]) {
		return ""
	}
	head := ""
	for _, event := range a.events[id][:count] {
		b, _ := json.Marshal(event)
		h := sha256.Sum256(append([]byte(head), b...))
		head = hex.EncodeToString(h[:])
	}
	return head
}

func eventDigest(previous string, event domain.Event) string {
	b, _ := json.Marshal(event)
	h := sha256.Sum256(append([]byte(previous), b...))
	return hex.EncodeToString(h[:])
}

type Inspection struct {
	Events             []domain.AuditTrailEvent
	CurrentHeadDigest  string
	ExpectedHeadDigest string
	Errors             []domain.AuditIntegrityError
}

// Inspect 每次从持久化 JSONL 读取事件，确保运行期间的文件删改也能被只读查询发现。
func (a *Audit) Inspect(id string) (Inspection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	events, readErrors, err := a.persistedEvents(id)
	if err != nil {
		return Inspection{}, err
	}
	result := Inspection{Events: make([]domain.AuditTrailEvent, 0, len(events)), ExpectedHeadDigest: a.heads[id], Errors: readErrors}
	previous := ""
	for index, event := range events {
		expectedRevision := int64(index + 1)
		if event.CaseID != id {
			result.Errors = append(result.Errors, domain.AuditIntegrityError{Revision: event.Revision, Reason: "审计事件 case_id 不匹配"})
		}
		if event.Revision != expectedRevision {
			result.Errors = append(result.Errors, domain.AuditIntegrityError{Revision: event.Revision, Reason: fmt.Sprintf("revision 不连续，期望 %d，实际 %d", expectedRevision, event.Revision)})
		}
		if !domain.AuditEventTypeAllowed(event.Type) {
			result.Errors = append(result.Errors, domain.AuditIntegrityError{Revision: event.Revision, Reason: "审计事件类型不在白名单"})
		}
		if !validTransition(events, index) {
			result.Errors = append(result.Errors, domain.AuditIntegrityError{Revision: event.Revision, Reason: "审计事件状态转换不连续"})
		}
		current := eventDigest(previous, event)
		result.Events = append(result.Events, domain.AuditTrailEvent{CaseID: event.CaseID, Revision: event.Revision, Type: event.Type, At: event.At, EvidenceDigest: event.EvidenceDigest, EvidenceDigests: event.EvidenceDigests, PreviousDigest: previous, EventDigest: current})
		previous = current
	}
	result.CurrentHeadDigest = previous
	if result.ExpectedHeadDigest != "" && result.ExpectedHeadDigest != previous {
		result.Errors = append(result.Errors, domain.AuditIntegrityError{Revision: int64(len(events)), Reason: "当前审计链头摘要不匹配", ExpectedDigest: result.ExpectedHeadDigest, ActualDigest: previous})
	}
	return result, nil
}

func (a *Audit) persistedEvents(id string) ([]domain.Event, []domain.AuditIntegrityError, error) {
	f, err := os.Open(a.path)
	if os.IsNotExist(err) {
		return []domain.Event{}, []domain.AuditIntegrityError{}, nil
	}
	if err != nil {
		return nil, nil, persistenceReadError("打开审计日志", a.path, err)
	}
	defer f.Close()
	events := []domain.Event{}
	errors := []domain.AuditIntegrityError{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := int64(0)
	for scanner.Scan() {
		line++
		var event domain.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			errors = append(errors, domain.AuditIntegrityError{Revision: line, Reason: "审计 JSONL 事件无法解析"})
			continue
		}
		if event.CaseID == id {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, persistenceReadError("读取审计日志", a.path, err)
	}
	return events, errors, nil
}

// ContinueHead 从可信前序摘要继续计算返回事件的链头。
func ContinueHead(previous string, events []domain.AuditTrailEvent) string {
	for _, event := range events {
		previous = eventDigest(previous, domain.Event{CaseID: event.CaseID, Revision: event.Revision, Type: event.Type, At: event.At, EvidenceDigest: event.EvidenceDigest, EvidenceDigests: event.EvidenceDigests})
	}
	return previous
}

// Page 返回经过完整连续性校验的审计页；页首 previous_digest 可证明与上一页相接。
func (a *Audit) Page(id string, afterRevision int64, limit int) (domain.AuditPage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := a.events[id]
	if afterRevision < 0 || afterRevision > int64(len(events)) || limit < 1 || limit > 100 {
		return domain.AuditPage{}, domain.ErrInvalid
	}
	trail := make([]domain.AuditTrailEvent, len(events))
	previous := ""
	for i, event := range events {
		if event.CaseID != id || event.Revision != int64(i+1) || !validTransition(events, i) {
			return domain.AuditPage{}, domain.ErrIntegrity
		}
		current := eventDigest(previous, event)
		trail[i] = domain.AuditTrailEvent{CaseID: event.CaseID, Revision: event.Revision, Type: event.Type, At: event.At, EvidenceDigest: event.EvidenceDigest, EvidenceDigests: event.EvidenceDigests, PreviousDigest: previous, EventDigest: current}
		previous = current
	}
	if previous != a.heads[id] {
		return domain.AuditPage{}, domain.ErrIntegrity
	}
	start := int(afterRevision)
	end := start + limit
	if end > len(trail) {
		end = len(trail)
	}
	pageEvents := append([]domain.AuditTrailEvent(nil), trail[start:end]...)
	next := int64(len(trail))
	if end < len(trail) && len(pageEvents) > 0 {
		next = pageEvents[len(pageEvents)-1].Revision
	}
	return domain.AuditPage{CaseID: id, Events: pageEvents, Filters: domain.AuditPageFilters{}, AfterRevision: afterRevision, NextAfterRevision: next, Limit: limit, HasMore: end < len(trail), ValidatedThroughRevision: int64(len(trail)), CurrentHeadDigest: previous, IntegrityStatus: "verified", Errors: []domain.AuditIntegrityError{}}, nil
}
