package application

import (
	"archiveflow/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type PreflightItem struct {
	Index                  int                            `json:"index"`
	Status                 string                         `json:"status"`
	AccessionCode          string                         `json:"accession_code,omitempty"`
	AlternativeIdentifiers []domain.AlternativeIdentifier `json:"alternative_identifiers,omitempty"`
	DuplicateIndices       []int                          `json:"duplicate_indices,omitempty"`
	Conflicts              []map[string]interface{}       `json:"conflicts,omitempty"`
	Errors                 map[string]string              `json:"errors,omitempty"`
}
type PreflightReport struct {
	RequestID    string            `json:"request_id"`
	ReportDigest string            `json:"report_digest"`
	InputDigest  string            `json:"input_digest"`
	Results      []PreflightItem   `json:"results"`
	AuditHeads   map[string]string `json:"audit_heads"`
	NoAuditEvent bool              `json:"no_audit_event"`
}

func (a *App) Preflight(requestID string, items []domain.RegistrationItem) (PreflightReport, error) {
	if strings.TrimSpace(requestID) == "" || len(items) == 0 || len(items) > 100 {
		return PreflightReport{}, domain.ErrInvalid
	}
	reqDigest, _ := digest(items)
	key := "preflight:" + requestID
	if b, ok := a.Store.GetIdempotency(key); ok {
		var saved struct {
			Digest string
			Report PreflightReport
		}
		if json.Unmarshal(b, &saved) != nil {
			return PreflightReport{}, domain.ErrIntegrity
		}
		if saved.Digest != reqDigest {
			return PreflightReport{}, domain.ErrConflict
		}
		return saved.Report, nil
	}
	out := PreflightReport{RequestID: requestID, InputDigest: reqDigest, Results: make([]PreflightItem, len(items)), AuditHeads: map[string]string{}, NoAuditEvent: true}
	seen := map[string][]int{}
	normalized := make([]string, len(items))
	for i, it := range items {
		r := PreflightItem{Index: i, Errors: map[string]string{}}
		acc, e := domain.NormalizeAccession(it.AccessionCode)
		if e != nil {
			r.Status = "invalid"
			r.Errors["accession_code"] = "格式无效"
		} else {
			normalized[i] = acc
			r.AccessionCode = acc
			seen[acc] = append(seen[acc], i)
		}
		if strings.TrimSpace(it.Title) == "" {
			r.Errors["title"] = "必填"
		}
		if strings.TrimSpace(it.RightsNote) == "" {
			r.Errors["rights_note"] = "必填"
		}
		if strings.TrimSpace(it.CarrierType) == "" {
			r.Errors["carrier_type"] = "必填"
		}
		if strings.TrimSpace(it.ContentScope) == "" {
			r.Errors["content_scope"] = "必填"
		}
		if ids, _, err := domain.NormalizeAlternativeIdentifiers(it.AlternativeIdentifiers); err != nil {
			r.Status = "invalid"
			r.Errors["alternative_identifiers"] = "格式无效"
		} else {
			r.AlternativeIdentifiers = ids
		}
		if r.Status == "" {
			r.Status = "ok"
		}
		out.Results[i] = r
	}
	for _, idx := range seen {
		if len(idx) > 1 {
			for _, i := range idx {
				out.Results[i].Status = "conflict"
				out.Results[i].DuplicateIndices = append([]int(nil), idx...)
			}
		}
	}
	identifierSeen := map[string][]int{}
	for i := range items {
		if out.Results[i].Status == "invalid" {
			continue
		}
		identifierSeen[normalized[i]] = append(identifierSeen[normalized[i]], i)
		for _, id := range out.Results[i].AlternativeIdentifiers {
			identifierSeen[id.Value] = append(identifierSeen[id.Value], i)
		}
	}
	for _, idx := range identifierSeen {
		if len(idx) > 1 {
			for _, i := range idx {
				out.Results[i].Status = "conflict"
				out.Results[i].DuplicateIndices = append([]int(nil), idx...)
			}
		}
	}
	for i := range items {
		if out.Results[i].Status == "invalid" {
			continue
		}
		ids := out.Results[i].AlternativeIdentifiers
		c, typ, val := a.Store.FindIdentifier(normalized[i], ids)
		if c != nil {
			out.Results[i].Status = "conflict"
			out.Results[i].Conflicts = []map[string]interface{}{{"existing_case_id": c.ID, "identifier_type": typ, "identifier_value": val, "normalized_value": val}}
			out.AuditHeads[c.ID] = a.Audit.Head(c.ID)
		}
	}
	raw, _ := json.Marshal(out.Results)
	h := sha256.Sum256(raw)
	out.ReportDigest = hex.EncodeToString(h[:])
	b, _ := json.Marshal(struct {
		Digest string
		Report PreflightReport
	}{reqDigest, out})
	if err := a.Store.PutIdempotency(key, b); err != nil {
		return PreflightReport{}, err
	}
	return out, nil
}
