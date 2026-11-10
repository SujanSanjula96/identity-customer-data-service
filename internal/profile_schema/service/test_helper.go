package service

import (
	"context"
)

func OverrideValidateApplicationIdentifierForTest(
	fn func(ctx context.Context, appIdentifier string, orgId string) (error, bool),
) (restore func()) {

	prev := validateApplicationIdentifierFn
	validateApplicationIdentifierFn = fn

	return func() {
		validateApplicationIdentifierFn = prev
	}
}
