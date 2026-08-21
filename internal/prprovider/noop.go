package prprovider

// NoopProvider is the default provider. It does NOT create a PR — it returns
// a Result with Number=0 and a synthetic URL. This preserves the prior
// behavior where CreatePR only rendered the body without network calls.
// Callers can check Result.Number == 0 to detect noop mode.
type NoopProvider struct{}

func (NoopProvider) CreatePR(req Request) (*Result, error) {
	return &Result{
		Number: 0,
		URL:    "", // empty = no real PR created
		State:  "noop",
	}, nil
}
