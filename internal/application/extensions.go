package application

import (
	"archiveflow/internal/domain"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CustodyChain performs a read-only custody chain projection and integrity check.
func (a *App) CustodyChain(id string, fromTime, toTime *time.Time, limit int, includeEvents bool) (domain.CustodyChainResult, error) {
	if limit < 0 || limit > 100 || (fromTime != nil && toTime != nil && toTime.Before(*fromTime)) {
		return domain.CustodyChainResult{}, domain.ErrInvalid
	}
	c, err := a.Store.Get(id)
	if err != nil {
		return domain.CustodyChainResult{}, err
	}
	result := domain.CustodyChainResult{
		CaseID: id, CurrentCustodian: c.CurrentCustodian, CurrentLocationCode: c.CurrentLocationCode,
		CustodyChainDigest: c.CustodyChainDigest, AuditHead: a.Audit.Head(id), IntegrityStatus: "verified", Errors: []string{}, Events: []domain.CustodyEvent{},
	}
	if len(c.CustodyEvents) > 0 {
		result.CurrentSealStatus = c.CustodyEvents[len(c.CustodyEvents)-1].SealStatus
		result.SealStatus = result.CurrentSealStatus
	}
	actual := domain.CustodyChainDigest(c.CustodyEvents)
	result.ExpectedDigest, result.ActualDigest = c.CustodyChainDigest, actual
	if actual != c.CustodyChainDigest {
		result.IntegrityStatus = "integrity_error"
		result.Errors = append(result.Errors, "custody_chain_digest 不匹配")
	}
	registeredAt := c.CreatedAt
	// Transfers appended after registration are valid; use the chain's final
	// timestamp as the normalization ceiling while still checking strict order.
	if len(c.CustodyEvents) > 0 && c.CustodyEvents[len(c.CustodyEvents)-1].OccurredAt.After(registeredAt) {
		registeredAt = c.CustodyEvents[len(c.CustodyEvents)-1].OccurredAt
	}
	if _, custodian, location, digest, normalizeErr := domain.NormalizeCustodyEvents(c.CustodyEvents, c.IntakeReceipt, registeredAt); normalizeErr != nil {
		result.IntegrityStatus = "integrity_error"
		result.Errors = append(result.Errors, normalizeErr.Error())
	} else {
		if digest != actual {
			result.IntegrityStatus = "integrity_error"
			result.Errors = append(result.Errors, "规范化交接链摘要不匹配")
		}
		if custodian != c.CurrentCustodian || location != c.CurrentLocationCode {
			result.IntegrityStatus = "integrity_error"
			result.Errors = append(result.Errors, "当前保管结论与交接链不一致")
		}
	}
	if !a.Audit.Validate(id, c.Revision) {
		result.IntegrityStatus = "integrity_error"
		result.Errors = append(result.Errors, "审计链连续性或摘要头校验失败")
	}
	for _, event := range a.Audit.Events(id) {
		if anchored, ok := event.EvidenceDigests["custody_chain"]; ok && anchored != actual {
			result.IntegrityStatus = "integrity_error"
			result.Errors = append(result.Errors, "审计事件 custody_chain 摘要锚点不匹配")
			break
		}
	}
	if c.Manifest != nil {
		if c.Manifest.CanonicalPayload.CustodyChainDigest != actual {
			result.IntegrityStatus = "integrity_error"
			result.Errors = append(result.Errors, "保存包清单 custody_chain_digest 不匹配")
		}
		if c.Manifest.AuditRevision != c.Revision-1 || c.Manifest.AuditHeadDigest != a.Audit.HeadAt(id, int(c.Manifest.AuditRevision)) {
			result.IntegrityStatus = "integrity_error"
			result.Errors = append(result.Errors, "保存包清单审计锚点不匹配")
		}
	}
	filtered := make([]domain.CustodyEvent, 0, len(c.CustodyEvents))
	for _, event := range c.CustodyEvents {
		at := event.OccurredAt.UTC()
		if fromTime != nil && at.Before(fromTime.UTC()) {
			continue
		}
		if toTime != nil && at.After(toTime.UTC()) {
			continue
		}
		filtered = append(filtered, event)
	}
	result.EventCount = len(filtered)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	if includeEvents {
		result.Events = filtered
	}
	result.ExpectedDigest = ""
	result.ActualDigest = ""
	if result.IntegrityStatus == "integrity_error" {
		result.ExpectedDigest, result.ActualDigest = c.CustodyChainDigest, actual
	}
	return result, nil
}

type batchIdempotencyEntry struct {
	RequestDigest string                         `json:"request_digest"`
	Result        domain.RegistrationBatchResult `json:"result"`
}

func (a *App) CreateBatch(requestID, mode string, items []domain.RegistrationItem) (domain.RegistrationBatchResult, error) {
	a.createMu.Lock()
	defer a.createMu.Unlock()
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "per_item" || mode == "item" {
		mode = "partial"
	}
	if requestID == "" || (mode != "atomic" && mode != "partial") || len(items) == 0 || len(items) > 100 {
		return domain.RegistrationBatchResult{}, domain.ErrInvalid
	}
	request := struct {
		Mode  string                    `json:"mode"`
		Items []domain.RegistrationItem `json:"items"`
	}{mode, items}
	requestDigest, err := digest(request)
	if err != nil {
		return domain.RegistrationBatchResult{}, err
	}
	key := "create-batch:" + requestID
	if saved, ok := a.Store.GetIdempotency(key); ok {
		var entry batchIdempotencyEntry
		if json.Unmarshal(saved, &entry) != nil {
			return domain.RegistrationBatchResult{}, domain.ErrIntegrity
		}
		if entry.RequestDigest != requestDigest {
			return domain.RegistrationBatchResult{}, domain.ErrConflict
		}
		// 前次调用可能已持久化幂等结果，但审计追加当时不可用；重新建立
		// 登记边界，使客户端重试无需创建第二个聚合。重试必须原样保留
		// 首次登记时间和接收凭证等证据摘要。
		for _, r := range entry.Result.Results {
			if r.Case == nil {
				continue
			}
			if !a.Audit.Validate(r.Case.ID, r.Case.Revision) {
				evidenceDigest, evidenceDigests := registrationEvidence(r.Case)
				if err := a.Audit.AppendEvidenceDigestsAt(r.Case.ID, "REGISTERED", r.Case.Revision, evidenceDigest, evidenceDigests, r.Case.FirstAuditAt); err != nil {
					return domain.RegistrationBatchResult{}, err
				}
			}
		}
		return entry.Result, nil
	}

	result := domain.RegistrationBatchResult{RequestID: requestID, Mode: mode, Results: make([]domain.RegistrationResult, len(items))}
	candidates := make([]*domain.DigitizationCase, len(items))
	positions := map[string][]int{}
	for i, item := range items {
		r := domain.RegistrationResult{Index: i, Errors: map[string]string{}}
		normalized, normalizeErr := domain.NormalizeAccession(item.AccessionCode)
		if normalizeErr != nil {
			r.Errors["accession_code"] = "格式无效"
		} else {
			r.AccessionCode = normalized
			positions[normalized] = append(positions[normalized], i)
		}
		for name, value := range map[string]string{"title": item.Title, "rights_note": item.RightsNote, "carrier_type": item.CarrierType, "content_scope": item.ContentScope} {
			if strings.TrimSpace(value) == "" {
				r.Errors[name] = "必填"
			}
		}
		if len(r.Errors) > 0 {
			r.Status = "invalid"
			result.Results[i] = r
			continue
		}
		id, idErr := randomCaseID()
		if idErr != nil {
			return domain.RegistrationBatchResult{}, idErr
		}
		candidate, createErr := domain.NewCaseWithCustodyEvidence(id, normalized, item.Title, item.RightsNote, item.CarrierType, item.ContentScope, item.IntakeReceipt, item.CarrierFacets, item.AlternativeIdentifiers, item.CustodyEvents)
		if createErr != nil {
			r.Status = "invalid"
			key := "item"
			var detail *domain.DetailError
			if errors.As(createErr, &detail) {
				if field, ok := detail.Details["field"].(string); ok && field != "" {
					key = field
				}
				if itemIndex, ok := detail.Details["item_index"].(int); ok {
					key += fmt.Sprintf("[%d]", itemIndex)
				}
			}
			r.Errors[key] = createErr.Error()
			result.Results[i] = r
			continue
		}
		candidates[i] = candidate
		result.Results[i] = r
	}
	for accession, indices := range positions {
		if len(indices) > 1 {
			for _, index := range indices {
				r := result.Results[index]
				r.Status = "conflict"
				r.AccessionCode = accession
				r.DuplicateIndices = append([]int(nil), indices...)
				result.Results[index] = r
				candidates[index] = nil
			}
		}
	}
	identifierPositions := map[string][]int{}
	for index, candidate := range candidates {
		if candidate == nil {
			continue
		}
		identifierPositions[candidate.AccessionCode] = append(identifierPositions[candidate.AccessionCode], index)
		for _, identifier := range candidate.AlternativeIdentifiers {
			identifierPositions[identifier.Value] = append(identifierPositions[identifier.Value], index)
		}
	}
	for value, indices := range identifierPositions {
		if len(indices) < 2 {
			continue
		}
		for _, index := range indices {
			r := result.Results[index]
			r.Status = "conflict"
			r.DuplicateIndices = append([]int(nil), indices...)
			r.Errors = map[string]string{"identifier_value": value}
			result.Results[index] = r
			candidates[index] = nil
		}
	}
	for i, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if existing, typ, value := a.Store.FindIdentifier(candidate.AccessionCode, candidate.AlternativeIdentifiers); existing != nil {
			r := result.Results[i]
			r.Status = "conflict"
			r.ExistingCaseID = existing.ID
			r.CaseID = existing.ID
			r.Errors = map[string]string{"identifier_type": typ, "identifier_value": value}
			result.Results[i] = r
			candidates[i] = nil
		}
	}
	hasFailure := false
	for _, r := range result.Results {
		if r.Status == "invalid" || r.Status == "conflict" {
			hasFailure = true
		}
	}
	toCreate := []*domain.DigitizationCase{}
	if !(mode == "atomic" && hasFailure) {
		for i, candidate := range candidates {
			if candidate == nil {
				continue
			}
			r := result.Results[i]
			r.Status = "created"
			r.CaseID = candidate.ID
			r.Case = candidate
			result.Results[i] = r
			toCreate = append(toCreate, candidate)
			result.Created++
		}
	} else {
		for i, candidate := range candidates {
			if candidate != nil {
				r := result.Results[i]
				r.Status = "invalid"
				r.Errors = map[string]string{"batch": "原子批次已中止"}
				result.Results[i] = r
			}
		}
	}
	entryBytes, err := json.Marshal(batchIdempotencyEntry{RequestDigest: requestDigest, Result: result})
	if err != nil {
		return domain.RegistrationBatchResult{}, err
	}
	if err = a.Store.CommitBatch(toCreate, key, entryBytes); err != nil {
		return domain.RegistrationBatchResult{}, err
	}
	for _, c := range toCreate {
		evidenceDigest, evidenceDigests := registrationEvidence(c)
		if err = a.Audit.AppendEvidenceDigestsAt(c.ID, "REGISTERED", c.Revision, evidenceDigest, evidenceDigests, c.FirstAuditAt); err != nil {
			return domain.RegistrationBatchResult{}, err
		}
	}
	return result, nil
}

func randomCaseID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("case-%x", b), nil
}

func (a *App) PreviewManifest(id string) (domain.ManifestPreview, error) {
	c, err := a.Store.Get(id)
	if err != nil {
		return domain.ManifestPreview{}, err
	}
	preview := domain.ManifestPreview{AuditHeadDigest: a.Audit.Head(id), AuditRevision: c.Revision, BlockingReasons: []string{}}
	if c.State != domain.StateQCPassed {
		preview.BlockingReasons = append(preview.BlockingReasons, "个案尚未通过质量复核")
	}
	if !a.Audit.Validate(id, c.Revision) {
		preview.BlockingReasons = append(preview.BlockingReasons, "审计链不连续")
	}
	if len(preview.BlockingReasons) > 0 {
		return preview, nil
	}
	manifest, err := c.BuildManifest(preview.AuditHeadDigest, "PREVIEW")
	if err != nil {
		preview.BlockingReasons = append(preview.BlockingReasons, err.Error())
		return preview, nil
	}
	// 候选清单不预断正式封存时间，保持重复预检逐字节稳定。
	manifest.SealedAt = time.Time{}
	preview.Sealable = true
	preview.CandidateManifestDigest = manifest.CanonicalPayloadDigest
	preview.CandidateManifest = &manifest
	preview.ComponentDigests = manifest.ComponentDigests
	return preview, nil
}

func (a *App) SealWithDigestRequest(req, id string, rev int64, by, expectedDigest string) (*domain.DigitizationCase, error) {
	payload := struct {
		SealedBy       string `json:"sealed_by"`
		ExpectedDigest string `json:"expected_manifest_digest"`
	}{by, expectedDigest}
	return a.mutateWithRequest(req, id, rev, payload, func(c *domain.DigitizationCase) error {
		if !a.Audit.Validate(id, c.Revision) {
			return domain.ErrIntegrity
		}
		manifest, err := c.BuildManifest(a.Audit.Head(id), by)
		if err != nil {
			return err
		}
		if strings.TrimSpace(expectedDigest) == "" || !strings.EqualFold(expectedDigest, manifest.CanonicalPayloadDigest) {
			return domain.Conflict("候选清单摘要已变化", map[string]interface{}{"candidate_manifest_digest": manifest.CanonicalPayloadDigest, "audit_revision": c.Revision})
		}
		return c.Seal(manifest)
	}, "SEALED")
}

func (a *App) ComponentProof(id, component string, generation int) (domain.ComponentProof, error) {
	c, err := a.Store.Get(id)
	if err != nil {
		return domain.ComponentProof{}, err
	}
	proof, err := c.BuildComponentProof(component, generation)
	if err != nil {
		return domain.ComponentProof{}, err
	}
	proof.Verification["audit_anchor"] = a.Audit.Validate(id, c.Revision) && c.Manifest != nil && c.Manifest.AuditHeadDigest == a.Audit.HeadAt(id, int(c.Manifest.AuditRevision))
	if !proof.Verification["audit_anchor"] && proof.MismatchLevel == "" {
		proof.MismatchLevel = "audit_anchor"
	}
	return proof, nil
}
