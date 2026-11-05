package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type agentProfileSubmission struct {
	RequestDigest string `json:"request_digest"`
	ProfileID     string `json:"profile_id,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
}

func AgentProfileSave(home string, req AgentProfileRequest) (AgentProfileCatalog, error) {
	return withAgentProfileSubmission(home, "profile", req.SubmissionID, req, func() (AgentProfileCatalog, error) { return saveAgentProfile(home, req) })
}

func AgentConnectionSave(home string, req AgentConnectionRequest) (AgentProfileCatalog, error) {
	if req.Kind != "" || req.BaseURL != "" || req.Token != "" {
		return AgentProfileCatalog{}, fmt.Errorf("The connection accepts a supported provider integration only.")
	}
	source, ok := agentSubscriptionIntegration(req.TemplateID)
	if !ok {
		return AgentProfileCatalog{}, fmt.Errorf("Select a supported connection.")
	}
	if agentAPIKeyKind(source.Kind) {
		if req.APIKey == "" {
			return AgentProfileCatalog{}, fmt.Errorf("Enter the API key.")
		}
		if _, err := normalizeProviderCredential("key-input", ProviderCredentialRecord{Credential: "key-input", Channel: source.Channel(), Type: "api_key", APIKey: req.APIKey}); err != nil {
			return AgentProfileCatalog{}, err
		}
	} else if req.APIKey != "" {
		return AgentProfileCatalog{}, fmt.Errorf("The sign-in connection does not accept an API key.")
	}
	return withAgentProfileSubmission(home, "connection", req.SubmissionID, req, func() (AgentProfileCatalog, error) {
		revision, err := validateAPIKeyReplacement(home, req, source)
		if err != nil {
			return AgentProfileCatalog{}, err
		}
		return saveAgentConnection(home, req, revision)
	})
}

func withAgentProfileSubmission(home, kind, submission string, request any, save func() (AgentProfileCatalog, error)) (AgentProfileCatalog, error) {
	if submission == "" || len(submission) > 128 {
		return AgentProfileCatalog{}, fmt.Errorf("The submission identifier is invalid. Start a new form.")
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	sum := sha256.Sum256([]byte(kind + ":" + submission))
	name := "submission-" + hex.EncodeToString(sum[:])
	lock, err := agentProfileOperationLock(root, name)
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	defer agentProfileUnlock(lock)
	requestBody, err := json.Marshal(request)
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	requestHash := sha256.Sum256(requestBody)
	clear(requestBody)
	digest := hex.EncodeToString(requestHash[:])
	path := filepath.Join(root, name+".receipt")
	if _, statErr := os.Lstat(path); statErr == nil {
		body, err := readSecretSource(path, "Agent profile submission")
		if err != nil {
			return AgentProfileCatalog{}, err
		}
		var receipt agentProfileSubmission
		if json.Unmarshal(body, &receipt) != nil {
			return AgentProfileCatalog{}, fmt.Errorf("The submission result is unavailable. Refresh the panel.")
		}
		if receipt.RequestDigest != digest {
			return AgentProfileCatalog{}, fmt.Errorf("This submission already completed with different values. Start a new submission.")
		}
		out, err := AgentProfileList(home)
		out.ProfileID = receipt.ProfileID
		out.ConnectionID = receipt.ConnectionID
		return out, err
	} else if !os.IsNotExist(statErr) {
		return AgentProfileCatalog{}, statErr
	}
	out, err := save()
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	body, err := json.Marshal(agentProfileSubmission{RequestDigest: digest, ProfileID: out.ProfileID, ConnectionID: out.ConnectionID})
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	file, err := os.CreateTemp(root, ".submission-")
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return AgentProfileCatalog{}, err
	}
	if err = os.Rename(temp, path); err != nil {
		return AgentProfileCatalog{}, err
	}
	if err = syncDirectory(root); err != nil {
		return AgentProfileCatalog{}, err
	}
	return out, nil
}
