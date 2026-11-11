package deployment

import "path/filepath"

// AgentProfileOperationKind reads the immutable kind without a status transition.
func AgentProfileOperationKind(home, id string) (string, error) {
	canonical, err := CanonicalHome(home)
	if err != nil {
		return "", err
	}
	record, err := readAgentProfileOperation(filepath.Join(canonical, "agent-profile-operations"), id)
	if err != nil {
		return "", err
	}
	return record.Kind, nil
}
