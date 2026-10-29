package engine

import (
	"encoding/json"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func accountingFailure(raw []byte) error {
	var problem struct {
		Code   string `json:"code"`
		Detail struct {
			Code string `json:"code"`
		} `json:"detail"`
	}
	if json.Unmarshal(raw, &problem) != nil {
		return nil
	}
	for _, code := range []string{problem.Code, problem.Detail.Code} {
		switch code {
		case "ACCOUNTING_DURABILITY_ERROR", "ACCOUNTING_AUTHORIZATION_ERROR", "accounting_durability_error", "billing_authorization_failed":
			return pkgerrors.ErrAccountingDurability
		}
	}
	return nil
}
