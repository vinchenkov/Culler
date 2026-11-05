//go:build !darwin

package deployment

// Platforms without a keychain leave the Claude CLI writing
// $CLAUDE_CONFIG_DIR/.credentials.json directly, which the existing bounded
// read already validates. No disposable credential store is needed.
type claudeCredentialStore struct{}

func prepareClaudeCredentialStore(string, []string) (*claudeCredentialStore, error) {
	return &claudeCredentialStore{}, nil
}

func (s *claudeCredentialStore) export(string) error { return nil }
