package domain

import "time"

type State string

const (
	StateRegistered State = "REGISTERED"
	StateAssessed   State = "ASSESSED"
	StateReady      State = "READY_FOR_CAPTURE"
	StateCaptured   State = "CAPTURED"
	StateRecapture  State = "RECAPTURE_REQUIRED"
	StateQCPassed   State = "QC_PASSED"
	StateSealed     State = "SEALED"
)

type DigitizationCase struct {
	ID                           string                  `json:"id"`
	AccessionCode                string                  `json:"accession_code"`
	AlternativeIdentifiers       []AlternativeIdentifier `json:"alternative_identifiers"`
	AlternativeIdentifierDigest  string                  `json:"alternative_identifier_digest"`
	Title                        string                  `json:"title"`
	RightsNote                   string                  `json:"rights_note"`
	CarrierType                  string                  `json:"carrier_type"`
	ContentScope                 string                  `json:"content_scope"`
	CarrierFacets                []CarrierFacet          `json:"carrier_facets"`
	CarrierFacetsDigest          string                  `json:"carrier_facets_digest"`
	State                        State                   `json:"state"`
	CurrentCaptureGeneration     int                     `json:"current_capture_generation"`
	Revision                     int64                   `json:"revision"`
	CreatedAt                    time.Time               `json:"created_at"`
	FirstAuditAt                 time.Time               `json:"first_audit_at"`
	IntakeReceipt                *IntakeReceipt          `json:"intake_receipt,omitempty"`
	SealedAt                     *time.Time              `json:"sealed_at,omitempty"`
	Assessment                   *ConditionAssessment    `json:"assessment,omitempty"`
	AssessmentHistory            []ConditionAssessment   `json:"assessment_history"`
	Plan                         *CapturePlan            `json:"plan,omitempty"`
	PlanHistory                  []CapturePlan           `json:"plan_history"`
	Captures                     []CaptureGeneration     `json:"captures"`
	Quality                      []QualityDecision       `json:"quality"`
	Recaptures                   []RecaptureAction       `json:"recaptures"`
	Manifest                     *PreservationManifest   `json:"manifest,omitempty"`
	MatchedIdentifierSource      *AlternativeIdentifier  `json:"matched_identifier_source,omitempty"`
	CurrentEscalationRequirement *RecaptureEscalation    `json:"current_escalation_requirement,omitempty"`
	CustodyEvents                []CustodyEvent          `json:"custody_events"`
	CurrentCustodian             string                  `json:"current_custodian,omitempty"`
	CurrentLocationCode          string                  `json:"current_location_code,omitempty"`
	CustodyChainDigest           string                  `json:"custody_chain_digest,omitempty"`
}

type CustodyEvent struct {
	Transferor   string    `json:"transferor"`
	Receiver     string    `json:"receiver"`
	OccurredAt   time.Time `json:"occurred_at"`
	LocationCode string    `json:"location_code"`
	SealStatus   string    `json:"seal_status"`
	Notes        string    `json:"notes"`
}

// CustodyChainResult is the read-only custody verification projection.
type CustodyChainResult struct {
	CaseID              string         `json:"case_id"`
	Events              []CustodyEvent `json:"events"`
	EventCount          int            `json:"event_count"`
	CurrentCustodian    string         `json:"current_custodian"`
	CurrentLocationCode string         `json:"current_location_code"`
	CurrentSealStatus   string         `json:"current_seal_status"`
	SealStatus          string         `json:"seal_status"`
	CustodyChainDigest  string         `json:"custody_chain_digest"`
	AuditHead           string         `json:"audit_head"`
	IntegrityStatus     string         `json:"integrity_status"`
	Errors              []string       `json:"errors"`
	ExpectedDigest      string         `json:"expected_digest,omitempty"`
	ActualDigest        string         `json:"actual_digest,omitempty"`
}

type AlternativeIdentifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type IntakeReceipt struct {
	TransferOrganization string    `json:"transfer_organization"`
	Transferor           string    `json:"transferor"`
	Receiver             string    `json:"receiver"`
	ReceivedAt           time.Time `json:"received_at"`
	BatchNumber          string    `json:"batch_number"`
	PackagingCondition   string    `json:"packaging_condition"`
	ReceiptDigest        string    `json:"receipt_digest"`
}

type CarrierFacet struct {
	FacetID       string `json:"facet_id"`
	Label         string `json:"label"`
	PhysicalOrder int    `json:"physical_order"`
	ContentScope  string `json:"content_scope"`
	Playable      bool   `json:"playable"`
}

type ConditionAssessment struct {
	CaseID                    string                  `json:"case_id"`
	Assessor                  string                  `json:"assessor"`
	MoldLevel                 string                  `json:"mold_level"`
	Breakage                  bool                    `json:"breakage"`
	Adhesion                  bool                    `json:"adhesion"`
	Contamination             bool                    `json:"contamination,omitempty"`
	ContaminationNotes        string                  `json:"contamination_notes"`
	PlaybackRisk              string                  `json:"playback_risk"`
	RequiredTreatment         string                  `json:"required_treatment"`
	AssessedAt                time.Time               `json:"assessed_at"`
	RiskSummary               string                  `json:"risk_summary,omitempty"`
	RiskCategories            []string                `json:"risk_categories"`
	TreatmentCoverage         map[string]string       `json:"treatment_coverage"`
	TreatmentEvidence         []TreatmentEvidence     `json:"treatment_evidence"`
	NoTreatmentRequired       bool                    `json:"no_treatment_required"`
	ObservationEvidence       []ObservationEvidence   `json:"observation_evidence,omitempty"`
	ObservationEvidenceDigest string                  `json:"observation_evidence_digest,omitempty"`
	Acclimatization           *Acclimatization        `json:"acclimatization,omitempty"`
	AssessmentVersion         int                     `json:"assessment_version"`
	CorrectionReason          string                  `json:"correction_reason,omitempty"`
	CorrectedBy               string                  `json:"corrected_by,omitempty"`
	CorrectedAt               *time.Time              `json:"corrected_at,omitempty"`
	DamageLocations           []DamageLocation        `json:"damage_locations"`
	DamageSummaries           []DamageCategorySummary `json:"damage_summaries"`
	DamageLocationDigest      string                  `json:"damage_location_digest,omitempty"`
}
type DamageLocation struct {
	FacetID          string  `json:"facet_id"`
	Category         string  `json:"category"`
	PhysicalLocation string  `json:"physical_location"`
	Severity         string  `json:"severity"`
	AffectedRatio    float64 `json:"affected_ratio"`
	ObservationNotes string  `json:"observation_notes"`
	EvidenceSummary  string  `json:"evidence_summary"`
}
type DamageCategorySummary struct {
	Category           string  `json:"category"`
	HighestSeverity    string  `json:"highest_severity"`
	TotalAffectedRatio float64 `json:"total_affected_ratio"`
	LocationCount      int     `json:"location_count"`
}
type Acclimatization struct {
	Required                     bool                     `json:"required"`
	MinimumTemperatureC          float64                  `json:"minimum_temperature_c"`
	MaximumTemperatureC          float64                  `json:"maximum_temperature_c"`
	MinimumRelativeHumidity      float64                  `json:"minimum_relative_humidity"`
	MaximumRelativeHumidity      float64                  `json:"maximum_relative_humidity"`
	MinimumStableDurationMinutes int64                    `json:"minimum_stable_duration_minutes"`
	Readings                     []AcclimatizationReading `json:"readings"`
	ReleaseDecision              string                   `json:"release_decision"`
	ReleaseReasons               []string                 `json:"release_reasons"`
	AbnormalReadings             []AcclimatizationReading `json:"abnormal_readings"`
	Digest                       string                   `json:"digest"`
}
type AcclimatizationReading struct {
	MeasuredAt       time.Time `json:"measured_at"`
	TemperatureC     float64   `json:"temperature_c"`
	RelativeHumidity float64   `json:"relative_humidity"`
	MeasuredBy       string    `json:"measured_by"`
	InstrumentID     string    `json:"instrument_id"`
}
type ObservationEvidence struct {
	RiskCategory string    `json:"risk_category"`
	EvidenceType string    `json:"evidence_type"`
	AssetDigest  string    `json:"asset_digest"`
	ObservedAt   time.Time `json:"observed_at"`
	RecordedBy   string    `json:"recorded_by"`
	Description  string    `json:"description"`
}
type TreatmentEvidence struct {
	Category        string    `json:"category"`
	Action          string    `json:"action"`
	PerformedBy     string    `json:"performed_by"`
	CompletedAt     time.Time `json:"completed_at"`
	EvidenceSummary string    `json:"evidence_summary"`
}
type CapturePlan struct {
	CaseID                   string         `json:"case_id"`
	PlaybackDevice           string         `json:"playback_device"`
	SignalChain              string         `json:"signal_chain"`
	TargetCodec              string         `json:"target_codec"`
	SampleRateHz             int            `json:"sample_rate_hz"`
	BitDepth                 int            `json:"bit_depth"`
	ChannelMap               string         `json:"channel_map"`
	Operator                 string         `json:"operator"`
	ApprovedBy               string         `json:"approved_by"`
	ApprovedAt               time.Time      `json:"approved_at"`
	PlanRevision             int64          `json:"plan_revision"`
	Fingerprint              string         `json:"plan_fingerprint"`
	RevisionReason           string         `json:"revision_reason,omitempty"`
	ChangedFields            []string       `json:"changed_fields,omitempty"`
	RiskControls             []RiskControl  `json:"risk_controls,omitempty"`
	NoAdditionalControls     bool           `json:"no_additional_controls"`
	RiskControlDigest        string         `json:"risk_control_digest,omitempty"`
	CoveredRiskCategories    []string       `json:"covered_risk_categories"`
	CaptureTasks             []CaptureTask  `json:"capture_tasks"`
	SkippedFacets            []SkippedFacet `json:"skipped_facets"`
	TaskCoverageDigest       string         `json:"task_coverage_digest"`
	EstimatedTotalDurationMs int64          `json:"estimated_total_duration_ms"`
	ValidUntil               time.Time      `json:"valid_until"`
	ValidityStatus           string         `json:"validity_status"`
	ReapprovalReason         string         `json:"reapproval_reason,omitempty"`
	ReapprovesPlanRevision   int64          `json:"reapproves_plan_revision,omitempty"`
	ScheduledStart           time.Time      `json:"scheduled_start,omitempty"`
	ScheduledEnd             time.Time      `json:"scheduled_end,omitempty"`
	ReservationStatus        string         `json:"reservation_status,omitempty"`
	ReservationConsumedAt    *time.Time     `json:"reservation_consumed_at,omitempty"`
	ReservationReleasedAt    *time.Time     `json:"reservation_released_at,omitempty"`
	ReservationReleasedBy    string         `json:"reservation_released_by,omitempty"`
	ReservationReleaseReason string         `json:"reservation_release_reason,omitempty"`
}
type CaptureTask struct {
	TaskID              string `json:"task_id"`
	FacetID             string `json:"facet_id"`
	ExecutionOrder      int    `json:"execution_order"`
	EstimatedDurationMs int64  `json:"estimated_duration_ms"`
	ContentStart        string `json:"content_start"`
	ContentEnd          string `json:"content_end"`
	ChannelMap          string `json:"channel_map"`
}
type SkippedFacet struct {
	FacetID string `json:"facet_id"`
	Reason  string `json:"reason"`
}
type RiskControl struct {
	RiskCategory       string `json:"risk_category"`
	ControlCategory    string `json:"control_category"`
	OperationalMeasure string `json:"operational_measure"`
	ResponsiblePerson  string `json:"responsible_person"`
	PreCaptureCheck    string `json:"pre_capture_check"`
}
type CaptureGeneration struct {
	CaseID                        string                   `json:"case_id"`
	Generation                    int                      `json:"generation"`
	CalibrationReference          string                   `json:"calibration_reference"`
	StartedAt                     time.Time                `json:"started_at"`
	EndedAt                       time.Time                `json:"ended_at"`
	AssetDigest                   string                   `json:"asset_digest"`
	DurationMs                    int64                    `json:"duration_ms"`
	PeakDBFS                      float64                  `json:"peak_dbfs"`
	PlanRevision                  int64                    `json:"plan_revision"`
	PlanFingerprint               string                   `json:"plan_fingerprint"`
	RecaptureReason               string                   `json:"recapture_reason,omitempty"`
	AuthorizedBy                  string                   `json:"authorized_by,omitempty"`
	CalibrationDevice             string                   `json:"calibration_device"`
	CalibratedAt                  time.Time                `json:"calibrated_at"`
	CalibrationValidUntil         time.Time                `json:"calibration_valid_until"`
	AssetSizeBytes                int64                    `json:"asset_size_bytes"`
	ContainerFormat               string                   `json:"container_format"`
	ActualCodec                   string                   `json:"actual_codec"`
	ActualSampleRateHz            int                      `json:"actual_sample_rate_hz"`
	ActualBitDepth                int                      `json:"actual_bit_depth"`
	ActualChannels                int                      `json:"actual_channels"`
	TechnicalEvidenceDigest       string                   `json:"technical_evidence_digest"`
	OperationEvents               []OperationEvent         `json:"operation_events,omitempty"`
	OperationTimelineDigest       string                   `json:"operation_timeline_digest,omitempty"`
	PausedDurationMs              int64                    `json:"paused_duration_ms"`
	CalculatedAudioDurationMs     int64                    `json:"calculated_audio_duration_ms"`
	CaptureTaskID                 string                   `json:"capture_task_id"`
	FixityAlgorithm               string                   `json:"fixity_algorithm,omitempty"`
	FixityChunkSizeBytes          int64                    `json:"fixity_chunk_size_bytes,omitempty"`
	FixityChunks                  []FixityChunk            `json:"fixity_chunks,omitempty"`
	FixityDigest                  string                   `json:"fixity_digest,omitempty"`
	FixityCombinationRule         string                   `json:"fixity_combination_rule,omitempty"`
	RecaptureAuthorizationVersion int                      `json:"recapture_authorization_version,omitempty"`
	CalibrationProfile            *CalibrationProfile      `json:"calibration_profile,omitempty"`
	CalibrationMeasurements       []CalibrationMeasurement `json:"calibration_measurements,omitempty"`
	CalibrationResults            []CalibrationResult      `json:"calibration_results,omitempty"`
	CalibrationPolicyVersion      string                   `json:"calibration_policy_version,omitempty"`
	CalibrationStatus             string                   `json:"calibration_status,omitempty"`
	CalibrationEvidenceDigest     string                   `json:"calibration_evidence_digest,omitempty"`
	FileSegments                  []FileSegment            `json:"file_segments,omitempty"`
	SegmentCombinationRule        string                   `json:"segment_combination_rule,omitempty"`
	RecaptureRemediationDigest    string                   `json:"recapture_remediation_digest,omitempty"`
}
type FileSegment struct {
	Sequence         int    `json:"segment_index"`
	SourceStartMs    int64  `json:"source_start_ms"`
	SourceEndMs      int64  `json:"source_end_ms"`
	DurationMs       int64  `json:"duration_ms"`
	AssetSizeBytes   int64  `json:"asset_size_bytes"`
	AssetDigest      string `json:"asset_digest"`
	StartsContinuous bool   `json:"starts_continuous"`
	EndsContinuous   bool   `json:"ends_continuous"`
}
type CalibrationProfile struct {
	ReferenceFrequencyHz float64 `json:"reference_frequency_hz"`
	FrequencyToleranceHz float64 `json:"frequency_tolerance_hz,omitempty"`
	LevelToleranceDB     float64 `json:"level_tolerance_db,omitempty"`
	ChannelDifferenceDB  float64 `json:"channel_difference_db,omitempty"`
}
type CalibrationMeasurement struct {
	Channel                     string    `json:"channel"`
	ReferenceFrequencyHz        float64   `json:"reference_frequency_hz"`
	MeasuredFrequencyHz         float64   `json:"measured_frequency_hz"`
	TargetLevelDBFS             float64   `json:"target_level_dbfs"`
	MeasuredLevelDBFS           float64   `json:"measured_level_dbfs"`
	MeasuredAt                  time.Time `json:"measured_at"`
	InstrumentID                string    `json:"instrument_id"`
	InstrumentCertificateDigest string    `json:"instrument_certificate_digest"`
}
type CalibrationResult struct {
	Channel              string  `json:"channel"`
	LevelDeviationDB     float64 `json:"level_deviation_db"`
	FrequencyDeviationHz float64 `json:"frequency_deviation_hz"`
	ChannelDifferenceDB  float64 `json:"channel_difference_db"`
	Passed               bool    `json:"passed"`
}
type FixityChunk struct {
	Index     int    `json:"index"`
	SizeBytes int64  `json:"size_bytes"`
	Digest    string `json:"digest"`
}
type OperationEvent struct {
	Type        string    `json:"type"`
	OccurredAt  time.Time `json:"occurred_at"`
	Operator    string    `json:"operator"`
	Description string    `json:"description"`
}
type QualityDecision struct {
	CaseID                  string               `json:"case_id"`
	Generation              int                  `json:"generation"`
	CompletenessPassed      bool                 `json:"completeness_passed"`
	ClippingEvents          int                  `json:"clipping_events"`
	DropoutEvents           int                  `json:"dropout_events"`
	ChannelMappingPassed    bool                 `json:"channel_mapping_passed"`
	DurationVarianceMs      int64                `json:"duration_variance_ms"`
	ListeningNotes          string               `json:"listening_notes"`
	Decision                string               `json:"decision"`
	Reviewer                string               `json:"reviewer"`
	ReviewedAt              time.Time            `json:"reviewed_at"`
	FailureCategories       []string             `json:"failure_categories,omitempty"`
	FailureSummary          string               `json:"failure_summary,omitempty"`
	DefectMarkers           []DefectMarker       `json:"defect_markers"`
	DefectSummary           string               `json:"defect_summary"`
	ListeningIntervals      []ListeningInterval  `json:"listening_intervals,omitempty"`
	ListeningCoverage       []ChannelCoverage    `json:"listening_coverage,omitempty"`
	ListeningCoverageDigest string               `json:"listening_coverage_digest,omitempty"`
	RemediationChecks       []RemediationCheck   `json:"remediation_checks,omitempty"`
	RemediationEffectDigest string               `json:"remediation_effect_digest,omitempty"`
	ResolvedCategories      []string             `json:"resolved_categories,omitempty"`
	PersistentCategories    []string             `json:"persistent_categories,omitempty"`
	NewCategories           []string             `json:"new_categories,omitempty"`
	ChannelMetrics          []ChannelMetric      `json:"channel_metrics,omitempty"`
	MeasurementProfile      *MeasurementProfile  `json:"measurement_profile,omitempty"`
	ThresholdVersion        string               `json:"threshold_version,omitempty"`
	MetricResults           []MetricResult       `json:"metric_results,omitempty"`
	QualityEvidenceDigest   string               `json:"quality_evidence_digest,omitempty"`
	QualityRevision         int64                `json:"quality_revision"`
	RequiresCountersign     bool                 `json:"requires_countersign"`
	CountersignReasons      []string             `json:"countersign_reasons,omitempty"`
	CountersignStatus       string               `json:"countersign_status,omitempty"`
	Countersigns            []QualityCountersign `json:"countersigns,omitempty"`
	CountersignForRevision  int64                `json:"countersign_for_revision,omitempty"`
	ConfirmedEvidenceDigest string               `json:"confirmed_evidence_digest,omitempty"`
	Adjudication            *QualityAdjudication `json:"adjudication,omitempty"`
	AdjudicationForRevision int64                `json:"adjudication_for_revision,omitempty"`
	Adjudicator             string               `json:"adjudicator,omitempty"`
	DisagreementResolutions map[string]string    `json:"disagreement_resolutions,omitempty"`
	DefectImpacts           []DefectImpact       `json:"defect_impacts"`
	DefectImpactDigest      string               `json:"defect_impact_digest,omitempty"`
}
type DefectImpact struct {
	DefectType         string  `json:"defect_type"`
	Channel            string  `json:"channel"`
	AffectedDurationMs int64   `json:"affected_duration_ms"`
	AffectedRatio      float64 `json:"affected_ratio"`
	MarkerCount        int     `json:"marker_count"`
	HighestSeverity    string  `json:"highest_severity"`
}
type QualityAdjudication struct {
	AdjudicationForRevision int64             `json:"adjudication_for_revision"`
	CountersignForRevision  int64             `json:"countersign_for_revision"`
	Adjudicator             string            `json:"adjudicator"`
	Decision                string            `json:"decision"`
	ListeningNotes          string            `json:"listening_notes"`
	ConfirmedEvidenceDigest string            `json:"confirmed_evidence_digest"`
	DisagreementResolutions map[string]string `json:"disagreement_resolutions"`
	AdjudicatedAt           time.Time         `json:"adjudicated_at"`
	FailureCategories       []string          `json:"failure_categories,omitempty"`
}
type ChannelMetric struct {
	Channel                string  `json:"channel"`
	DCOffset               float64 `json:"dc_offset"`
	IntegratedLoudnessLUFS float64 `json:"integrated_loudness_lufs"`
	NoiseFloorDBFS         float64 `json:"noise_floor_dbfs"`
	SilenceRatio           float64 `json:"silence_ratio"`
}
type MeasurementProfile struct {
	Tool             string `json:"tool"`
	ToolVersion      string `json:"tool_version"`
	ParametersDigest string `json:"parameters_digest"`
}
type MetricResult struct {
	Channel string   `json:"channel"`
	Metric  string   `json:"metric"`
	Value   float64  `json:"value"`
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
	Passed  bool     `json:"passed"`
}
type QualityCountersign struct {
	CountersignForRevision  int64     `json:"countersign_for_revision"`
	Reviewer                string    `json:"reviewer"`
	Decision                string    `json:"decision"`
	ListeningNotes          string    `json:"listening_notes"`
	ConfirmedEvidenceDigest string    `json:"confirmed_evidence_digest"`
	ReviewedAt              time.Time `json:"reviewed_at"`
	Agreement               bool      `json:"agreement"`
	Disagreements           []string  `json:"disagreements,omitempty"`
	CountersignRevision     int64     `json:"countersign_revision"`
}
type ListeningInterval struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Channel string `json:"channel"`
	Method  string `json:"method"`
}
type ChannelCoverage struct {
	Channel       string  `json:"channel"`
	CoveredMs     int64   `json:"covered_ms"`
	CoverageRatio float64 `json:"coverage_ratio"`
}
type RemediationCheck struct {
	Category            string `json:"category"`
	Result              string `json:"result"`
	VerifiedBy          string `json:"verified_by"`
	EvidenceDescription string `json:"evidence_description"`
}
type DefectMarker struct {
	DefectType  string `json:"defect_type"`
	PositionMs  int64  `json:"position_ms"`
	DurationMs  int64  `json:"duration_ms"`
	Channel     string `json:"channel"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}
type CategoryRemediation struct {
	Category           string    `json:"category"`
	Action             string    `json:"action"`
	Owner              string    `json:"owner"`
	CompletionCriteria string    `json:"completion_criteria"`
	PerformedBy        string    `json:"performed_by,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
	Result             string    `json:"result,omitempty"`
	EvidenceDigest     string    `json:"evidence_digest,omitempty"`
	VerificationMethod string    `json:"verification_method,omitempty"`
}
type RecaptureAction struct {
	Action                    string                `json:"action"`
	AuthorizationVersion      int                   `json:"authorization_version"`
	Status                    string                `json:"status"`
	Reason                    string                `json:"reason"`
	Remediation               string                `json:"remediation"`
	AuthorizedBy              string                `json:"authorized_by"`
	Generation                int                   `json:"generation"`
	FailedQualityGeneration   int                   `json:"failed_quality_generation"`
	RequestedFailedGeneration int                   `json:"failed_generation,omitempty"`
	At                        time.Time             `json:"at"`
	Remediations              []CategoryRemediation `json:"remediations"`
	AuthorizedAt              time.Time             `json:"authorized_at"`
	ExpiresAt                 time.Time             `json:"expires_at"`
	ConsumedAt                *time.Time            `json:"consumed_at,omitempty"`
	ConsumedGeneration        int                   `json:"consumed_generation,omitempty"`
	RevokedBy                 string                `json:"revoked_by,omitempty"`
	RevokedAt                 *time.Time            `json:"revoked_at,omitempty"`
	RevocationReason          string                `json:"revocation_reason,omitempty"`
	RenewsVersion             int                   `json:"renews_version,omitempty"`
	Escalation                *RecaptureEscalation  `json:"escalation,omitempty"`
	RemediationEvidenceDigest string                `json:"remediation_evidence_digest,omitempty"`
}
type RecaptureEscalation struct {
	Required                  bool     `json:"required"`
	TriggeredCategories       []string `json:"triggered_categories"`
	FailureGenerations        []int    `json:"failure_generations"`
	PreservationOfficer       string   `json:"preservation_officer"`
	RiskDisposition           string   `json:"risk_disposition"`
	MaximumAdditionalAttempts int      `json:"maximum_additional_attempts"`
	RemainingAttempts         int      `json:"remaining_attempts"`
}
type PreservationManifest struct {
	CaseID                  string               `json:"case_id"`
	ManifestVersion         string               `json:"manifest_version"`
	CanonicalPayload        ManifestPayload      `json:"canonical_payload"`
	CanonicalPayloadDigest  string               `json:"canonical_payload_digest"`
	AuditHeadDigest         string               `json:"audit_head_digest"`
	AuditRevision           int64                `json:"audit_revision"`
	CaptureDigests          []string             `json:"capture_digests"`
	SealedBy                string               `json:"sealed_by"`
	SealedAt                time.Time            `json:"sealed_at"`
	VerificationStatus      string               `json:"verification_status"`
	ComponentDigests        ComponentDigests     `json:"component_digests"`
	GenerationEvidenceIndex []GenerationEvidence `json:"generation_evidence_index"`
}
type GenerationEvidence struct {
	Generation                    int    `json:"generation"`
	PlanRevision                  int64  `json:"plan_revision"`
	CaptureTaskID                 string `json:"capture_task_id"`
	TaskOrder                     int    `json:"task_order"`
	AssetDigest                   string `json:"asset_digest"`
	QualityRevision               int64  `json:"quality_revision"`
	QualityDecision               string `json:"quality_decision"`
	FailedQualityRevision         int64  `json:"failed_quality_revision,omitempty"`
	RecaptureAuthorizationVersion int    `json:"recapture_authorization_version,omitempty"`
}
type ComponentDigests struct {
	Registration string `json:"registration"`
	Assessment   string `json:"assessment"`
	Plans        string `json:"plans"`
	Captures     string `json:"captures"`
	Quality      string `json:"quality"`
	Recaptures   string `json:"recaptures"`
}

// ManifestPayload 的字段顺序即保存包的规范序列化顺序。
type ManifestPayload struct {
	ID                          string                  `json:"id"`
	AccessionCode               string                  `json:"accession_code"`
	AlternativeIdentifiers      []AlternativeIdentifier `json:"alternative_identifiers"`
	AlternativeIdentifierDigest string                  `json:"alternative_identifier_digest"`
	Title                       string                  `json:"title"`
	RightsNote                  string                  `json:"rights_note"`
	CarrierType                 string                  `json:"carrier_type"`
	ContentScope                string                  `json:"content_scope"`
	IntakeReceipt               *IntakeReceipt          `json:"intake_receipt"`
	Assessment                  *ConditionAssessment    `json:"assessment"`
	AssessmentHistory           []ConditionAssessment   `json:"assessment_history"`
	Plan                        *CapturePlan            `json:"plan"`
	PlanHistory                 []CapturePlan           `json:"plan_history"`
	Captures                    []CaptureGeneration     `json:"captures"`
	Quality                     []QualityDecision       `json:"quality"`
	Recaptures                  []RecaptureAction       `json:"recaptures"`
	CarrierFacets               []CarrierFacet          `json:"carrier_facets"`
	CarrierFacetsDigest         string                  `json:"carrier_facets_digest"`
	GenerationEvidenceIndex     []GenerationEvidence    `json:"generation_evidence_index"`
	CustodyEvents               []CustodyEvent          `json:"custody_events"`
	CurrentCustodian            string                  `json:"current_custodian"`
	CurrentLocationCode         string                  `json:"current_location_code"`
	CustodyChainDigest          string                  `json:"custody_chain_digest"`
}

type Event struct {
	CaseID          string            `json:"case_id"`
	Revision        int64             `json:"revision"`
	Type            string            `json:"type"`
	At              time.Time         `json:"at"`
	EvidenceDigest  string            `json:"evidence_digest,omitempty"`
	EvidenceDigests map[string]string `json:"evidence_digests,omitempty"`
}

type AuditTrailEvent struct {
	CaseID          string            `json:"case_id"`
	Revision        int64             `json:"revision"`
	Type            string            `json:"type"`
	At              time.Time         `json:"at"`
	EvidenceDigest  string            `json:"evidence_digest,omitempty"`
	EvidenceDigests map[string]string `json:"evidence_digests,omitempty"`
	PreviousDigest  string            `json:"previous_digest"`
	EventDigest     string            `json:"event_digest"`
}

type AuditPage struct {
	CaseID                    string                `json:"case_id"`
	Events                    []AuditTrailEvent     `json:"events"`
	Filters                   AuditPageFilters      `json:"filters"`
	AfterRevision             int64                 `json:"after_revision"`
	NextAfterRevision         int64                 `json:"next_after_revision"`
	Limit                     int                   `json:"limit"`
	HasMore                   bool                  `json:"has_more"`
	ValidatedThroughRevision  int64                 `json:"validated_through_revision"`
	CurrentHeadDigest         string                `json:"current_head_digest"`
	ExpectedCurrentHeadDigest string                `json:"expected_current_head_digest,omitempty"`
	ActualCurrentHeadDigest   string                `json:"actual_current_head_digest,omitempty"`
	IntegrityStatus           string                `json:"integrity_status"`
	Errors                    []AuditIntegrityError `json:"errors"`
	ResponseDigest            string                `json:"response_digest,omitempty"`
}

// AuditPageFilters 是审计检索的规范化只读筛选条件。
type AuditPageFilters struct {
	FromTime  *time.Time `json:"from_time,omitempty"`
	ToTime    *time.Time `json:"to_time,omitempty"`
	EventType string     `json:"event_type,omitempty"`
}

type AuditQuery struct {
	FromTime      *time.Time
	ToTime        *time.Time
	EventType     string
	AfterRevision int64
	Limit         int
}

// AuditIntegrityError 保留链断点及摘要差异，供外部复核定位。
type AuditIntegrityError struct {
	Revision       int64  `json:"revision,omitempty"`
	Reason         string `json:"reason"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
	ActualDigest   string `json:"actual_digest,omitempty"`
}

type RegistrationItem struct {
	AccessionCode          string                  `json:"accession_code"`
	Title                  string                  `json:"title"`
	RightsNote             string                  `json:"rights_note"`
	CarrierType            string                  `json:"carrier_type"`
	ContentScope           string                  `json:"content_scope"`
	IntakeReceipt          *IntakeReceipt          `json:"intake_receipt,omitempty"`
	CarrierFacets          []CarrierFacet          `json:"carrier_facets,omitempty"`
	AlternativeIdentifiers []AlternativeIdentifier `json:"alternative_identifiers,omitempty"`
	CustodyEvents          []CustodyEvent          `json:"custody_events,omitempty"`
}

type RegistrationResult struct {
	Index            int               `json:"index"`
	Status           string            `json:"status"`
	CaseID           string            `json:"case_id,omitempty"`
	AccessionCode    string            `json:"accession_code,omitempty"`
	ExistingCaseID   string            `json:"existing_case_id,omitempty"`
	DuplicateIndices []int             `json:"duplicate_indices,omitempty"`
	Errors           map[string]string `json:"errors,omitempty"`
	Case             *DigitizationCase `json:"case,omitempty"`
}

type RegistrationBatchResult struct {
	RequestID string               `json:"request_id"`
	Mode      string               `json:"mode"`
	Results   []RegistrationResult `json:"results"`
	Created   int                  `json:"created_count"`
}

type ManifestPreview struct {
	Sealable                bool                  `json:"sealable"`
	CandidateManifestDigest string                `json:"candidate_manifest_digest,omitempty"`
	AuditHeadDigest         string                `json:"audit_head_digest"`
	AuditRevision           int64                 `json:"audit_revision"`
	BlockingReasons         []string              `json:"blocking_reasons"`
	CandidateManifest       *PreservationManifest `json:"candidate_manifest,omitempty"`
	ComponentDigests        ComponentDigests      `json:"component_digests"`
}

type ManifestVerification struct {
	Valid                bool                     `json:"valid"`
	Status               string                   `json:"status"`
	MismatchedComponents []string                 `json:"mismatched_components"`
	ReferenceErrors      []EvidenceReferenceError `json:"reference_errors"`
	ExpectedDigest       string                   `json:"expected_digest"`
	ActualDigest         string                   `json:"actual_digest"`
}

type IntegrityCheckResult struct {
	CaseID               string                   `json:"case_id"`
	AccessionCode        string                   `json:"accession_code"`
	Status               string                   `json:"status"`
	MismatchedComponents []string                 `json:"mismatched_components"`
	ReferenceErrors      []EvidenceReferenceError `json:"reference_errors"`
	AuditError           string                   `json:"audit_error,omitempty"`
	ExpectedDigest       string                   `json:"expected_digest,omitempty"`
	ActualDigest         string                   `json:"actual_digest,omitempty"`
}

type IntegrityCheckStats struct {
	PageValid             int `json:"page_valid"`
	PageInvalid           int `json:"page_invalid"`
	PageUnavailable       int `json:"page_unavailable"`
	TotalValid            int `json:"total_valid"`
	TotalInvalid          int `json:"total_invalid"`
	TotalUnavailable      int `json:"total_unavailable"`
	ValidCount            int `json:"valid_count"`
	InvalidCount          int `json:"invalid_count"`
	UnavailableCount      int `json:"unavailable_count"`
	TotalValidCount       int `json:"total_valid_count"`
	TotalInvalidCount     int `json:"total_invalid_count"`
	TotalUnavailableCount int `json:"total_unavailable_count"`
}

type ComponentProof struct {
	Component              string          `json:"component"`
	Generation             int             `json:"generation,omitempty"`
	Content                interface{}     `json:"content"`
	ComponentDigest        string          `json:"component_digest"`
	ProofPath              []string        `json:"proof_path"`
	CanonicalPayloadDigest string          `json:"canonical_payload_digest"`
	Verification           map[string]bool `json:"verification"`
	MismatchLevel          string          `json:"mismatch_level,omitempty"`
}
type EvidenceReferenceError struct {
	Generation int    `json:"generation,omitempty"`
	Field      string `json:"field"`
	Message    string `json:"message"`
}
