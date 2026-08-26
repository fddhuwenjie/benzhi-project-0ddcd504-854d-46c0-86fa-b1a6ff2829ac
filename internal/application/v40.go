package application

import (
	"archiveflow/internal/domain"
	"sync"
)

func (a *App) IntegrityChecks(page, all []*domain.DigitizationCase) ([]domain.IntegrityCheckResult, domain.IntegrityCheckStats) {
	results := make([]domain.IntegrityCheckResult, len(page))
	stats := domain.IntegrityCheckStats{}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, c := range page {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result := a.integrityCheck(c)
			results[index] = result
			addIntegrityCount(&stats, result.Status, true)
		}()
	}
	for _, c := range all {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result := a.integrityCheck(c)
			addIntegrityCount(&stats, result.Status, false)
		}()
	}
	close(start)
	workers.Wait()
	stats.ValidCount, stats.InvalidCount, stats.UnavailableCount = stats.PageValid, stats.PageInvalid, stats.PageUnavailable
	stats.TotalValidCount, stats.TotalInvalidCount, stats.TotalUnavailableCount = stats.TotalValid, stats.TotalInvalid, stats.TotalUnavailable
	return results, stats
}

func (a *App) integrityCheck(c *domain.DigitizationCase) domain.IntegrityCheckResult {
	result := domain.IntegrityCheckResult{CaseID: c.ID, AccessionCode: c.AccessionCode, MismatchedComponents: []string{}, ReferenceErrors: []domain.EvidenceReferenceError{}}
	if c.State != domain.StateSealed || c.Manifest == nil {
		result.Status = "UNAVAILABLE"
		result.AuditError = "保存包清单不可用"
		return result
	}
	verification := c.ManifestVerification()
	result.MismatchedComponents = verification.MismatchedComponents
	result.ReferenceErrors = verification.ReferenceErrors
	result.ExpectedDigest, result.ActualDigest = verification.ExpectedDigest, verification.ActualDigest
	auditValid := a.Audit.Validate(c.ID, c.Revision)
	anchorValid := c.Manifest.AuditRevision == c.Revision-1 && c.Manifest.AuditHeadDigest == a.Audit.HeadAt(c.ID, int(c.Manifest.AuditRevision))
	if !auditValid {
		result.AuditError = "审计事件链不连续"
	} else if !anchorValid {
		result.AuditError = "保存包审计锚点不匹配"
	}
	if verification.Valid && c.VerifyManifest() && result.AuditError == "" {
		result.Status = "VALID"
	} else {
		result.Status = "INVALID"
	}
	return result
}

func addIntegrityCount(stats *domain.IntegrityCheckStats, status string, page bool) {
	if page {
		switch status {
		case "VALID":
			stats.PageValid++
		case "INVALID":
			stats.PageInvalid++
		default:
			stats.PageUnavailable++
		}
		return
	}
	switch status {
	case "VALID":
		stats.TotalValid++
	case "INVALID":
		stats.TotalInvalid++
	default:
		stats.TotalUnavailable++
	}
}
