package application

import (
	"archiveflow/internal/audit"
	"archiveflow/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type auditSearchCacheKey struct {
	caseID        string
	fromTime      string
	toTime        string
	eventType     string
	afterRevision int64
	limit         int
}

// AuditSearch 在完整链上校验后按原始 revision 游标筛选，且不产生任何写入。
func (a *App) AuditSearch(id string, query domain.AuditQuery) (domain.AuditPage, error) {
	unlock := a.lock(id)
	defer unlock()
	if err := domain.ValidateAuditQuery(query); err != nil {
		return domain.AuditPage{}, err
	}
	eventType, _ := domain.NormalizeAuditEventType(query.EventType)
	query.EventType = eventType
	query.FromTime = utcTime(query.FromTime)
	query.ToTime = utcTime(query.ToTime)
	c, err := a.Store.Get(id)
	if err != nil {
		return domain.AuditPage{}, err
	}
	if query.AfterRevision > c.Revision {
		return domain.AuditPage{}, domain.Invalid("after_revision 超出当前 revision", map[string]interface{}{"current_revision": c.Revision})
	}
	cacheKey := auditSearchCacheKey{caseID: id, eventType: query.EventType, afterRevision: query.AfterRevision, limit: query.Limit}
	if query.FromTime != nil {
		cacheKey.fromTime = query.FromTime.Format(time.RFC3339Nano)
	}
	if query.ToTime != nil {
		cacheKey.toTime = query.ToTime.Format(time.RFC3339Nano)
	}
	if cached, ok := a.auditSearches[cacheKey]; ok {
		return cached, nil
	}
	inspection, err := a.Audit.Inspect(id)
	if err != nil {
		return domain.AuditPage{}, err
	}
	errors := append([]domain.AuditIntegrityError{}, inspection.Errors...)
	if int64(len(inspection.Events)) != c.Revision {
		errors = appendAuditError(errors, domain.AuditIntegrityError{Revision: firstRevisionDifference(inspection.Events, c.Revision), Reason: fmt.Sprintf("审计事件数量与个案 revision 不一致，期望 %d，实际 %d", c.Revision, len(inspection.Events))})
	}
	expectedHead := inspection.ExpectedHeadDigest
	if c.State == domain.StateSealed && c.Manifest != nil {
		actualAnchor := headAtRevision(inspection.Events, c.Manifest.AuditRevision)
		if c.Manifest.AuditRevision != c.Revision-1 {
			errors = appendAuditError(errors, domain.AuditIntegrityError{Revision: c.Manifest.AuditRevision, Reason: "封存清单审计 revision 与个案不一致"})
		}
		if actualAnchor != c.Manifest.AuditHeadDigest {
			errors = appendAuditError(errors, domain.AuditIntegrityError{Revision: c.Manifest.AuditRevision, Reason: "封存清单审计锚点不匹配", ExpectedDigest: c.Manifest.AuditHeadDigest, ActualDigest: actualAnchor})
		}
		tail := make([]domain.AuditTrailEvent, 0, 1)
		for _, event := range inspection.Events {
			if event.Revision > c.Manifest.AuditRevision {
				tail = append(tail, event)
			}
		}
		expectedHead = audit.ContinueHead(c.Manifest.AuditHeadDigest, tail)
	}
	if expectedHead == "" {
		expectedHead = inspection.CurrentHeadDigest
	}
	if expectedHead != inspection.CurrentHeadDigest {
		errors = appendAuditError(errors, domain.AuditIntegrityError{Revision: c.Revision, Reason: "current_head_digest 与可信审计锚点不匹配", ExpectedDigest: expectedHead, ActualDigest: inspection.CurrentHeadDigest})
	}

	matching := make([]domain.AuditTrailEvent, 0, query.Limit)
	matchingAfterPage := false
	for _, event := range inspection.Events {
		if event.Revision <= query.AfterRevision || !auditEventMatches(event, query) {
			continue
		}
		if len(matching) < query.Limit {
			matching = append(matching, event)
		} else {
			matchingAfterPage = true
			break
		}
	}
	next := c.Revision
	if matchingAfterPage && len(matching) > 0 {
		next = matching[len(matching)-1].Revision
	}
	validated := c.Revision
	for _, integrityError := range errors {
		if integrityError.Revision > 0 && integrityError.Revision-1 < validated {
			validated = integrityError.Revision - 1
		}
	}
	status := "verified"
	if len(errors) > 0 {
		status = "integrity_error"
	}
	page := domain.AuditPage{
		CaseID: id, Events: matching, Filters: domain.AuditPageFilters{FromTime: query.FromTime, ToTime: query.ToTime, EventType: query.EventType},
		AfterRevision: query.AfterRevision, NextAfterRevision: next, Limit: query.Limit, HasMore: matchingAfterPage,
		ValidatedThroughRevision: validated, CurrentHeadDigest: inspection.CurrentHeadDigest, IntegrityStatus: status, Errors: errors,
	}
	if status == "integrity_error" {
		page.ExpectedCurrentHeadDigest = expectedHead
		page.ActualCurrentHeadDigest = inspection.CurrentHeadDigest
	}
	payload, err := json.Marshal(page)
	if err != nil {
		return domain.AuditPage{}, err
	}
	digest := sha256.Sum256(payload)
	page.ResponseDigest = hex.EncodeToString(digest[:])
	a.auditSearches[cacheKey] = page
	return page, nil
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func auditEventMatches(event domain.AuditTrailEvent, query domain.AuditQuery) bool {
	at := event.At.UTC()
	if query.FromTime != nil && at.Before(*query.FromTime) {
		return false
	}
	if query.ToTime != nil && at.After(*query.ToTime) {
		return false
	}
	return query.EventType == "" || event.Type == query.EventType
}

func headAtRevision(events []domain.AuditTrailEvent, revision int64) string {
	if revision == 0 {
		return ""
	}
	for _, event := range events {
		if event.Revision == revision {
			return event.EventDigest
		}
	}
	return ""
}

func firstRevisionDifference(events []domain.AuditTrailEvent, upper int64) int64 {
	for index, event := range events {
		if event.Revision != int64(index+1) {
			return int64(index + 1)
		}
	}
	if int64(len(events)) < upper {
		return int64(len(events) + 1)
	}
	return upper
}

func appendAuditError(errors []domain.AuditIntegrityError, next domain.AuditIntegrityError) []domain.AuditIntegrityError {
	for _, existing := range errors {
		if existing.Revision == next.Revision && existing.Reason == next.Reason && existing.ExpectedDigest == next.ExpectedDigest && existing.ActualDigest == next.ActualDigest {
			return errors
		}
	}
	return append(errors, next)
}
