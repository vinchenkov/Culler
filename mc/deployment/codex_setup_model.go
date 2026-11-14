package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	operatorconfig "mc/config"
)

// CodexSetupModelError identifies a connection test without a verified model name.
type CodexSetupModelError struct{ Detail string }

func (e *CodexSetupModelError) Error() string {
	message := "Setup cannot verify the model for this Codex connection."
	if e.Detail != "" {
		message += " " + e.Detail
	}
	return message
}

func verifiedCodexModel(path string, output []byte) (string, error) {
	completed := false
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			Event  string `json:"event"`
			Status string `json:"status"`
			Output string `json:"output"`
			Item   struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Item.Type == "error" && strings.HasPrefix(event.Item.Message, "model rerouted:") {
			return "", &CodexSetupModelError{Detail: "Codex changed models during the connection test."}
		}
		if event.Event == "turn-complete" && event.Status == "success" && strings.TrimSpace(event.Output) == "MC_RUNTIME_AUTH_OK" {
			completed = true
		}
	}
	if !completed {
		return "", &CodexSetupModelError{Detail: "The connection test returned no successful verification reply."}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	const limit = 1 << 20
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if len(body) > limit {
		return "", &CodexSetupModelError{Detail: "The native trace exceeded the model verification limit."}
	}
	selected := ""
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "turn_context" {
			continue
		}
		model := event.Payload.Model
		if err := operatorconfig.ValidateBindingShape("setup", operatorconfig.RuntimeBinding{Harness: "codex", Model: model}, operatorconfig.CredentialKindCodexOAuth); err != nil || selected != "" && selected != model {
			return "", &CodexSetupModelError{Detail: "The native trace lacks one valid model selection."}
		}
		selected = model
	}
	if selected == "" {
		return "", &CodexSetupModelError{Detail: "The native trace contains no model selection."}
	}
	return selected, nil
}
