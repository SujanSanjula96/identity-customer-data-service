package service

import "context"

func OverrideValidateApplicationIdentifierForTest(
	fn func(context.Context, string, string) (error, bool),
) (restore func()) {

	prev := validateApplicationIdentifierFn
	validateApplicationIdentifierFn = fn

	return func() {
		validateApplicationIdentifierFn = prev
	}
}
