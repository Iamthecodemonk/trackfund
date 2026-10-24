package shared

import "errors"

var (
	ErrOwnerRequired       = errors.New("fund owner is required")
	ErrNameRequired        = errors.New("fund name is required")
	ErrTargetAmountInvalid = errors.New("target amount must be greater than zero")
	ErrCurrencyInvalid     = errors.New("currency must be a 3-letter ISO code")
	ErrContributionInvalid = errors.New("contribution amount must be greater than zero")
	ErrContributorRequired = errors.New("contributor is required")
)
