package service

import (
	"context"

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
)

func OverrideValidateApplicationIdentifierForTest(
	fn func(context.Context, string, orgModel.Ref) (error, bool),
) (restore func()) {

	prev := validateApplicationIdentifierFn
	validateApplicationIdentifierFn = fn

	return func() {
		validateApplicationIdentifierFn = prev
	}
}
