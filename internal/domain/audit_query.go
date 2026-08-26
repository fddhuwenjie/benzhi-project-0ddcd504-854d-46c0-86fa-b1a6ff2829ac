package domain

import (
	"sort"
	"strings"
)

var auditEventTypes = map[string]struct{}{
	"REGISTERED": {}, "ASSESSED": {}, "ASSESSMENT_CORRECTED": {},
	"PLAN_APPROVED": {}, "PLAN_REVISED": {}, "PLAN_REAPPROVED": {},
	"PLAN_RESERVATION_RELEASED": {}, "CAPTURED": {}, "QUALITY": {},
	"QUALITY_ADJUDICATED": {}, "RECAPTURE_AUTHORIZED": {},
	"RECAPTURE_REVOKED": {}, "RECAPTURE_RENEWED": {},
	"CUSTODY_TRANSFER": {}, "SEALED": {},
}

func NormalizeAuditEventType(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "", true
	}
	_, ok := auditEventTypes[value]
	return value, ok
}

func AuditEventTypeAllowed(value string) bool {
	_, ok := auditEventTypes[value]
	return ok
}

func AuditEventTypeValues() []string {
	values := make([]string, 0, len(auditEventTypes))
	for value := range auditEventTypes {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func ValidateAuditQuery(query AuditQuery) error {
	if query.AfterRevision < 0 {
		return Invalid("after_revision 无效", map[string]interface{}{"minimum": 0})
	}
	if query.Limit < 1 || query.Limit > 100 {
		return Invalid("limit 无效", map[string]interface{}{"minimum": 1, "maximum": 100})
	}
	if query.FromTime != nil && query.ToTime != nil && query.ToTime.Before(*query.FromTime) {
		return Invalid("from_time 不得晚于 to_time", nil)
	}
	if _, ok := NormalizeAuditEventType(query.EventType); !ok {
		return Invalid("未知 event_type", map[string]interface{}{"allowed_event_types": AuditEventTypeValues()})
	}
	return nil
}
