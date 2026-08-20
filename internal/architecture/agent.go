package architecture

// AgentCheck is the governance hook agents call before touching files. It
// returns the violations for the files the agent intends to change, scoped to
// exactly those files. The governance firewall treats error-severity
// violations as high risk.
func AgentCheck(root string, proposedFiles []string) (*Report, error) {
	return ValidateDiff(root, proposedFiles)
}
