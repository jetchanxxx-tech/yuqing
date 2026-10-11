package accountclosure

import (
	"github.com/yuqing/platform/internal/platform/billing"
	"regexp"
)

var auditID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,127}$`)

func retainedAuditString(key, value string) bool {
	switch key {
	case "target_id", "ledger_id", "user_id", "tenant_id", "actor_id", "analysis_id", "order_id", "consume_tx_id", "run_id":
		return auditID.MatchString(value)
	case "target_type":
		return value == "user" || value == "tenant" || value == "member" || value == "analysis" || value == "order"
	case "role":
		return value == "viewer" || value == "analyst" || value == "tenant_admin" || value == "platform_admin"
	case "status":
		switch value {
		case "active", "disabled", "pending_activation", "closure_pending", "closed", "suspended", "provisioning":
			return true
		}
	case "plan_code", "effective_plan_code":
		return billing.DefaultPlans()[value] != nil
	case "plan_source":
		return value == "report_credits" || value == "purchase" || value == "manual" || value == "default_free"
	}
	return false
}

// Immutable linkage and validated categories survive; personal/free-form
// fields and arbitrary strings do not become an accidental PII allowlist.
func ScrubAudit(value any) any { return scrubAuditField("", value) }
func scrubAuditField(key string, value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			switch k {
			case "name", "email", "phone", "nickname", "avatar_url", "reason", "reason_detail", "request_id", "ip_address", "user_agent":
				continue
			}
			if result := scrubAuditField(k, x); result != nil {
				out[k] = result
			}
		}
		out["anonymized"] = true
		return out
	case []any:
		out := []any{}
		for _, x := range v {
			category := key
			if key == "platform_roles" {
				category = "role"
			}
			if result := scrubAuditField(category, x); result != nil {
				out = append(out, result)
			}
		}
		return out
	case string:
		if retainedAuditString(key, v) {
			return v
		}
	case int, int64, float64, bool:
		return v
	}
	return nil
}
