package store

import (
	"archiveflow/internal/domain"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Store struct {
	dir   string
	mu    sync.RWMutex
	cases map[string]*domain.DigitizationCase
	idem  map[string][]byte
}

type snapshot struct {
	Cases       map[string]*domain.DigitizationCase `json:"cases"`
	Idempotency map[string][]byte                   `json:"idempotency"`
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, cases: map[string]*domain.DigitizationCase{}, idem: map[string][]byte{}}
	if b, e := os.ReadFile(filepath.Join(dir, "snapshot.json")); e == nil {
		var saved snapshot
		if json.Unmarshal(b, &saved) == nil {
			if saved.Cases != nil {
				s.cases = saved.Cases
			}
			if saved.Idempotency != nil {
				s.idem = saved.Idempotency
			}
		}
	} else {
		if b, readErr := os.ReadFile(filepath.Join(dir, "cases.json")); readErr == nil {
			_ = json.Unmarshal(b, &s.cases)
		}
		if b, readErr := os.ReadFile(filepath.Join(dir, "idempotency.json")); readErr == nil {
			_ = json.Unmarshal(b, &s.idem)
		}
	}
	return s, nil
}
func (s *Store) GetIdempotency(id string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.idem[id]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}
func (s *Store) PutIdempotency(id string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneBytesMap(s.idem)
	next[id] = append([]byte(nil), value...)
	if err := s.saveSnapshot(s.cases, next); err != nil {
		return err
	}
	s.idem = next
	return nil
}
func (s *Store) saveSnapshot(cases map[string]*domain.DigitizationCase, idem map[string][]byte) error {
	b, err := json.MarshalIndent(snapshot{Cases: cases, Idempotency: idem}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "snapshot.tmp")
	if e := os.WriteFile(tmp, b, 0644); e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(s.dir, "snapshot.json"))
}
func (s *Store) Get(id string) (*domain.DigitizationCase, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	b, _ := json.Marshal(c)
	var cp domain.DigitizationCase
	json.Unmarshal(b, &cp)
	return &cp, nil
}
func (s *Store) Put(c *domain.DigitizationCase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneCases(s.cases)
	next[c.ID] = cloneCase(c)
	if err := s.saveSnapshot(next, s.idem); err != nil {
		return err
	}
	s.cases = next
	return nil
}

// Commit 在同一个持久化快照内保存个案及幂等响应。
func (s *Store) Commit(c *domain.DigitizationCase, idempotencyKey string, response []byte, create bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if create {
		for _, existing := range s.cases {
			if _, _, ok := identifierConflict(existing, c.AccessionCode, c.AlternativeIdentifiers); ok {
				return domain.ErrConflict
			}
		}
	} else {
		existing, ok := s.cases[c.ID]
		if !ok {
			return domain.ErrNotFound
		}
		if existing.Revision+1 != c.Revision {
			return domain.ErrConflict
		}
	}
	nextCases := cloneCases(s.cases)
	nextCases[c.ID] = cloneCase(c)
	nextIdem := cloneBytesMap(s.idem)
	if idempotencyKey != "" {
		nextIdem[idempotencyKey] = append([]byte(nil), response...)
	}
	if err := s.saveSnapshot(nextCases, nextIdem); err != nil {
		return err
	}
	s.cases, s.idem = nextCases, nextIdem
	return nil
}

// CommitBatch 在单个快照内原子保存一批新个案及批次幂等回执。
func (s *Store) CommitBatch(cases []*domain.DigitizationCase, idempotencyKey string, response []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	accessions := map[string]bool{}
	for _, existing := range s.cases {
		accessions[existing.AccessionCode] = true
		for _, identifier := range existing.AlternativeIdentifiers {
			accessions[identifier.Value] = true
		}
	}
	for _, c := range cases {
		if accessions[c.AccessionCode] {
			return domain.ErrConflict
		}
		accessions[c.AccessionCode] = true
		for _, identifier := range c.AlternativeIdentifiers {
			if accessions[identifier.Value] {
				return domain.ErrConflict
			}
			accessions[identifier.Value] = true
		}
	}
	nextCases := cloneCases(s.cases)
	for _, c := range cases {
		nextCases[c.ID] = cloneCase(c)
	}
	nextIdem := cloneBytesMap(s.idem)
	if idempotencyKey != "" {
		nextIdem[idempotencyKey] = append([]byte(nil), response...)
	}
	if err := s.saveSnapshot(nextCases, nextIdem); err != nil {
		return err
	}
	s.cases, s.idem = nextCases, nextIdem
	return nil
}
func (s *Store) Create(c *domain.DigitizationCase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.cases {
		if x.AccessionCode == c.AccessionCode {
			return domain.ErrConflict
		}
	}
	next := cloneCases(s.cases)
	next[c.ID] = cloneCase(c)
	if err := s.saveSnapshot(next, s.idem); err != nil {
		return err
	}
	s.cases = next
	return nil
}

func cloneCases(source map[string]*domain.DigitizationCase) map[string]*domain.DigitizationCase {
	next := make(map[string]*domain.DigitizationCase, len(source)+1)
	for key, value := range source {
		next[key] = value
	}
	return next
}

func cloneCase(source *domain.DigitizationCase) *domain.DigitizationCase {
	b, _ := json.Marshal(source)
	var cloned domain.DigitizationCase
	_ = json.Unmarshal(b, &cloned)
	return &cloned
}

func cloneBytesMap(source map[string][]byte) map[string][]byte {
	next := make(map[string][]byte, len(source)+1)
	for key, value := range source {
		next[key] = append([]byte(nil), value...)
	}
	return next
}

func (s *Store) FindByAccession(accession string) *domain.DigitizationCase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.cases {
		if c.AccessionCode == accession {
			b, _ := json.Marshal(c)
			var cp domain.DigitizationCase
			_ = json.Unmarshal(b, &cp)
			return &cp
		}
	}
	return nil
}

func identifierConflict(existing *domain.DigitizationCase, accession string, alternatives []domain.AlternativeIdentifier) (string, string, bool) {
	if existing.AccessionCode == accession {
		return "ACCESSION_CODE", accession, true
	}
	for _, old := range existing.AlternativeIdentifiers {
		if old.Value == accession {
			return old.Type, old.Value, true
		}
		for _, next := range alternatives {
			if old.Value == next.Value {
				return old.Type, old.Value, true
			}
		}
	}
	for _, next := range alternatives {
		if existing.AccessionCode == next.Value {
			return "ACCESSION_CODE", next.Value, true
		}
	}
	return "", "", false
}

func (s *Store) FindIdentifier(accession string, alternatives []domain.AlternativeIdentifier) (*domain.DigitizationCase, string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.cases {
		if typ, value, ok := identifierConflict(c, accession, alternatives); ok {
			return cloneCase(c), typ, value
		}
	}
	return nil, "", ""
}

func (s *Store) CaptureEvidenceConflict(reference, device string, calibratedAt, validUntil time.Time, assetDigest string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.cases {
		for _, capture := range c.Captures {
			if assetDigest != "" && strings.EqualFold(capture.AssetDigest, assetDigest) {
				return "asset_digest", true
			}
			for _, segment := range capture.FileSegments {
				if assetDigest != "" && strings.EqualFold(segment.AssetDigest, assetDigest) {
					return "file_segments.asset_digest", true
				}
			}
			if reference != "" && capture.CalibrationReference == reference && (!strings.EqualFold(capture.CalibrationDevice, device) || !capture.CalibratedAt.Equal(calibratedAt) || !capture.CalibrationValidUntil.Equal(validUntil)) {
				return "calibration_reference", true
			}
		}
	}
	return "", false
}

type ResourceConflict struct {
	CaseID       string    `json:"case_id"`
	ResourceType string    `json:"resource_type"`
	Resource     string    `json:"resource"`
	Start        time.Time `json:"conflict_start"`
	End          time.Time `json:"conflict_end"`
}

func (s *Store) PlanResourceConflict(caseID string, plan domain.CapturePlan) *ResourceConflict {
	all := s.PlanResourceConflicts(caseID, plan)
	if len(all) > 0 {
		return &all[0]
	}
	return nil
}

func (s *Store) PlanResourceConflicts(caseID string, plan domain.CapturePlan) []ResourceConflict {
	if plan.ScheduledStart.IsZero() || plan.ScheduledEnd.IsZero() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	conflicts := []ResourceConflict{}
	for _, c := range s.cases {
		if c.ID == caseID || c.State == domain.StateSealed || c.Plan == nil {
			continue
		}
		other := c.Plan
		if other.ScheduledStart.IsZero() || other.ScheduledEnd.IsZero() || other.ReservationStatus == "CONSUMED" || other.ReservationStatus == "RELEASED" || now.After(other.ScheduledEnd) || now.After(other.ValidUntil) {
			continue
		}
		if !plan.ScheduledStart.Before(other.ScheduledEnd) || !other.ScheduledStart.Before(plan.ScheduledEnd) {
			continue
		}
		start, end := plan.ScheduledStart, plan.ScheduledEnd
		if other.ScheduledStart.After(start) {
			start = other.ScheduledStart
		}
		if other.ScheduledEnd.Before(end) {
			end = other.ScheduledEnd
		}
		if strings.EqualFold(plan.PlaybackDevice, other.PlaybackDevice) {
			// continue scanning to report all overlapping resources
			conflicts = append(conflicts, ResourceConflict{CaseID: c.ID, ResourceType: "playback_device", Resource: other.PlaybackDevice, Start: start, End: end})
		}
		if strings.EqualFold(plan.Operator, other.Operator) {
			conflicts = append(conflicts, ResourceConflict{CaseID: c.ID, ResourceType: "operator", Resource: other.Operator, Start: start, End: end})
		}
	}
	return conflicts
}

type Filter struct {
	State                 string
	AccessionPrefix       string
	Title                 string
	FailureCategory       string
	SealedAfter           *time.Time
	SealedBefore          *time.Time
	AlternativeIdentifier string
	MinimumSeverity       string
	RiskCategory          string
	PlaybackRisk          string
	AssessmentVersion     int
	TreatmentStatus       string
	Offset, Limit         int
}

func (s *Store) Search(f Filter) ([]*domain.DigitizationCase, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*domain.DigitizationCase, 0)
	for _, c := range s.cases {
		if f.State != "" && string(c.State) != f.State {
			continue
		}
		if f.AccessionPrefix != "" && !strings.HasPrefix(c.AccessionCode, f.AccessionPrefix) {
			continue
		}
		if f.AlternativeIdentifier != "" {
			matched := false
			for _, identifier := range c.AlternativeIdentifiers {
				if identifier.Value == f.AlternativeIdentifier {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if f.Title != "" && !strings.Contains(strings.ToLower(c.Title), strings.ToLower(f.Title)) {
			continue
		}
		if f.SealedAfter != nil && (c.SealedAt == nil || c.SealedAt.Before(*f.SealedAfter)) {
			continue
		}
		if f.SealedBefore != nil && (c.SealedAt == nil || c.SealedAt.After(*f.SealedBefore)) {
			continue
		}
		if f.FailureCategory != "" && !hasFailureCategory(c, f.FailureCategory) {
			continue
		}
		if f.MinimumSeverity != "" && !hasMinimumSeverity(c, f.MinimumSeverity) {
			continue
		}
		if f.RiskCategory != "" || f.PlaybackRisk != "" || f.AssessmentVersion > 0 || f.TreatmentStatus != "" {
			if c.Assessment == nil {
				continue
			}
			a := c.Assessment
			if f.PlaybackRisk != "" && !strings.EqualFold(a.PlaybackRisk, f.PlaybackRisk) {
				continue
			}
			if f.AssessmentVersion > 0 && a.AssessmentVersion != f.AssessmentVersion {
				continue
			}
			if f.RiskCategory != "" {
				found := false
				for _, rc := range a.RiskCategories {
					base := rc
					if i := strings.Index(base, ":"); i >= 0 {
						base = base[:i]
					}
					if strings.EqualFold(rc, f.RiskCategory) || strings.EqualFold(base, f.RiskCategory) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			if f.TreatmentStatus != "" && treatmentStatus(a) != f.TreatmentStatus {
				continue
			}
		}
		b, _ := json.Marshal(c)
		var cp domain.DigitizationCase
		_ = json.Unmarshal(b, &cp)
		if f.AlternativeIdentifier != "" {
			for i := range cp.AlternativeIdentifiers {
				if cp.AlternativeIdentifiers[i].Value == f.AlternativeIdentifier {
					source := cp.AlternativeIdentifiers[i]
					cp.MatchedIdentifierSource = &source
					break
				}
			}
		}
		all = append(all, &cp)
	}
	// deterministic ordering by accession then id
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].AccessionCode < all[i].AccessionCode || (all[j].AccessionCode == all[i].AccessionCode && all[j].ID < all[i].ID) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	total := len(all)
	start := f.Offset
	if start < 0 {
		start = 0
	}
	if start > len(all) {
		start = len(all)
	}
	end := len(all)
	if f.Limit > 0 && start+f.Limit < end {
		end = start + f.Limit
	}
	return all[start:end], total
}

func treatmentStatus(a *domain.ConditionAssessment) string {
	if a == nil {
		return "pending"
	}
	required := map[string]bool{}
	if strings.ToLower(a.MoldLevel) != "none" {
		required["mold"] = true
	}
	if a.Breakage {
		required["breakage"] = true
	}
	if a.Adhesion {
		required["adhesion"] = true
	}
	if a.Contamination {
		required["contamination"] = true
	}
	if strings.ToLower(a.PlaybackRisk) == "high" {
		required["playback"] = true
	}
	if len(required) == 0 && a.NoTreatmentRequired {
		return "completed"
	}
	for r := range required {
		if _, ok := a.TreatmentCoverage[r]; !ok {
			return "pending"
		}
	}
	if a.Acclimatization != nil && a.Acclimatization.Required && a.Acclimatization.ReleaseDecision != "RELEASED" {
		return "completed"
	}
	return "ready"
}

func hasMinimumSeverity(c *domain.DigitizationCase, minimum string) bool {
	for _, q := range c.Quality {
		if q.Decision != "FAIL" {
			continue
		}
		for _, impact := range q.DefectImpacts {
			if domain.SeverityAtLeast(impact.HighestSeverity, minimum) {
				return true
			}
		}
	}
	return false
}

func (s *Store) List(f Filter) []*domain.DigitizationCase {
	items, _ := s.Search(f)
	return items
}

func hasFailureCategory(c *domain.DigitizationCase, category string) bool {
	for _, q := range c.Quality {
		for _, current := range q.FailureCategories {
			if current == category {
				return true
			}
		}
	}
	return false
}
func (s *Store) Count() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.cases) }
