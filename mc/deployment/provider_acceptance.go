package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mc/routing"
)

const (
	liveProviderToolMarker = "MC_LIVE_PROVIDER_TOOL_OK"
	liveProviderTurnMarker = "MC_LIVE_PROVIDER_TURN_OK"
)

type LiveProviderObservation struct {
	SelectedModel          string `json:"-"`
	ProfileAuthorizationID string `json:"-"`
	SessionID              string `json:"session_id"`
	NativeFile             string `json:"native_file"`
	StreamEvents           int    `json:"stream_events"`
	ToolUses               int    `json:"tool_uses"`
	ToolResults            int    `json:"tool_results"`
	// ProviderRequests counts requests that the host proxy sent.
	ProviderRequests int `json:"provider_requests,omitempty"`
}

type LiveProviderResult struct {
	Status           string `json:"status"`
	Binding          string `json:"binding"`
	Credential       string `json:"credential,omitempty"`
	Model            string `json:"model,omitempty"`
	AttemptID        string `json:"attempt_id,omitempty"`
	ReleaseBuildID   string `json:"release_build_id,omitempty"`
	Delivery         string `json:"delivery,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	NativeFile       string `json:"native_file,omitempty"`
	StreamEvents     int    `json:"stream_events,omitempty"`
	ToolUses         int    `json:"tool_uses,omitempty"`
	ProviderRequests int    `json:"provider_requests,omitempty"`
}

// maxLiveProviderRequests bounds the provider requests of one claude-sdk
// acceptance turn: one for the tool call and one for the final reply.
const maxLiveProviderRequests = 2

func validateLiveProviderIdentity(home, release, binding string) error {
	if !validProductionBuildID(release) {
		return fmt.Errorf("live-provider acceptance requires the immutable production release identity")
	}
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		return err
	}
	if _, ok := catalog.Binding(binding); !ok {
		return fmt.Errorf("unknown runtime binding %q", binding)
	}
	return nil
}

// validateEnabledRuntimeBinding checks that the canonical grant store holds
// exactly the grants of the configured credential sources it names, and that
// the binding's credential source is among them.
func validateEnabledRuntimeBinding(home, binding string, environment []string) (routing.BindingSpec, error) {
	var empty routing.BindingSpec
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		return empty, err
	}
	spec, ok := catalog.Binding(binding)
	if !ok {
		return empty, fmt.Errorf("unknown runtime binding %q", binding)
	}
	if err := validateRuntimeAuthEnvironment(environment); err != nil {
		return empty, err
	}
	store := filepath.Join(home, "refresh-grants")
	if err := requireOwnerOnlyDirectory(store, "runtime grant store"); err != nil {
		return empty, err
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return empty, err
	}
	credentials := make([]string, 0, len(entries))
	selected := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" ||
			filepath.Base(name) != name {
			return empty, fmt.Errorf("runtime grant store contains unexpected entry %q", name)
		}
		id := strings.TrimSuffix(name, ".json")
		if _, ok := catalog.Credentials[id]; !ok {
			return empty, fmt.Errorf("runtime grant store contains unknown credential %q", id)
		}
		credentials = append(credentials, id)
		selected = selected || id == spec.Credential
	}
	sort.Strings(credentials)
	if len(credentials) == 0 {
		return empty, fmt.Errorf("runtime grant store holds no credential")
	}
	if err := validateStagedRuntimeGrants(store, catalog, credentials); err != nil {
		return empty, err
	}
	if !selected {
		return empty, fmt.Errorf("runtime binding %q has no credential in the canonical grant store", binding)
	}
	return spec, nil
}

func validLiveProviderObservation(observation LiveProviderObservation) error {
	if observation.SessionID == "" || observation.NativeFile == "" ||
		filepath.IsAbs(observation.NativeFile) ||
		filepath.Clean(observation.NativeFile) != observation.NativeFile ||
		observation.NativeFile == ".." ||
		strings.HasPrefix(observation.NativeFile, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("live-provider observation lacks a safe durable native session")
	}
	if observation.StreamEvents <= 0 {
		return fmt.Errorf("live-provider observation lacks streaming evidence")
	}
	if observation.ToolUses != 1 || observation.ToolResults != 1 {
		return fmt.Errorf("live-provider observation must prove exactly one successful tool use")
	}
	return nil
}

func stringField(value any, name string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	text, _ := object[name].(string)
	return text
}

func numberField(value any, name string) (int, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	number, ok := object[name].(float64)
	return int(number), ok && number == float64(int(number))
}

func contentBlocks(message any) []any {
	object, ok := message.(map[string]any)
	if !ok {
		return nil
	}
	blocks, _ := object["content"].([]any)
	return blocks
}

func toolResultHasMarker(block map[string]any) bool {
	if value, ok := block["content"].(string); ok {
		return strings.Contains(value, liveProviderToolMarker)
	}
	for _, value := range contentBlocks(block) {
		if object, ok := value.(map[string]any); ok &&
			strings.Contains(stringField(object, "text"), liveProviderToolMarker) {
			return true
		}
	}
	return false
}

// InspectLiveProviderOutput consumes the bounded adapter event stream. It
// accepts provider-native stream/tool shapes only; the final assistant text
// cannot spoof tool evidence.
func InspectLiveProviderOutput(harness string, output []byte) (LiveProviderObservation, error) {
	var observation LiveProviderObservation
	var claudeDelta bool
	var sequence, sessionStarts, turnCompletes, turnCompleteAt int
	var codexThreadID, codexToolID string
	var codexThreadStarts, codexTurnStarts, codexTurnCompletes int
	var codexToolStarts, codexToolCompletes int
	var codexToolCompleteAt, codexFinalAt, codexTurnCompleteAt int
	var claudeInitID, claudeToolID string
	var claudeInits, claudeResults, claudeFinals int
	var claudeToolCompleteAt, claudeFinalAt int
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		sequence++
		if event["event"] == "session-start" {
			sessionStarts++
			if id, ok := event["session_id"].(string); ok {
				observation.SessionID = id
			}
			if native, ok := event["native_file"].(string); ok {
				observation.NativeFile = native
			}
		}
		if event["event"] == "turn-complete" {
			turnCompletes++
			if event["status"] == "success" &&
				event["output"] == liveProviderTurnMarker {
				turnCompleteAt = sequence
			}
		}
		eventType, _ := event["type"].(string)
		if harness == "codex" {
			if eventType == "thread.started" {
				codexThreadStarts++
				codexThreadID, _ = event["thread_id"].(string)
			}
			if eventType == "turn.started" {
				codexTurnStarts++
				observation.StreamEvents++
			} else if eventType == "turn.completed" {
				codexTurnCompletes++
				codexTurnCompleteAt = sequence
				observation.StreamEvents++
			} else if strings.HasPrefix(eventType, "item.") {
				observation.StreamEvents++
			}
			if eventType == "turn.failed" || eventType == "error" {
				return observation, fmt.Errorf("Codex adapter reported a provider error")
			}
			item, _ := event["item"].(map[string]any)
			if stringField(item, "type") == "agent_message" &&
				eventType == "item.completed" &&
				stringField(item, "text") == liveProviderTurnMarker {
				codexFinalAt = sequence
			}
			if stringField(item, "type") != "command_execution" {
				continue
			}
			switch eventType {
			case "item.started":
				codexToolStarts++
				observation.ToolUses++
				codexToolID = stringField(item, "id")
				if codexToolID == "" {
					codexToolID = stringField(event, "item_id")
				}
			case "item.completed":
				codexToolCompletes++
				completedID := stringField(item, "id")
				if completedID == "" {
					completedID = stringField(event, "item_id")
				}
				if completedID != codexToolID {
					continue
				}
			default:
				continue
			}
			if eventType != "item.completed" {
				continue
			}
			exitCode, ok := numberField(item, "exit_code")
			outputText := stringField(item, "aggregated_output")
			if outputText == "" {
				outputText = stringField(item, "output")
			}
			if ok && exitCode == 0 &&
				stringField(item, "status") == "completed" &&
				strings.Contains(outputText, liveProviderToolMarker) {
				observation.ToolResults++
				codexToolCompleteAt = sequence
			}
			continue
		}
		if eventType == "stream_event" {
			observation.StreamEvents++
			native, _ := event["event"].(map[string]any)
			switch stringField(native, "type") {
			case "content_block_delta":
				claudeDelta = true
			case "message_start":
				observation.ProviderRequests++
				if observation.ProviderRequests > maxLiveProviderRequests {
					return observation, fmt.Errorf("acceptance exceeded %d provider requests", maxLiveProviderRequests)
				}
			}
		}
		if eventType == "system" && event["subtype"] == "init" {
			claudeInits++
			claudeInitID, _ = event["session_id"].(string)
		}
		if eventType == "system" && event["subtype"] == "mirror_error" {
			return observation, fmt.Errorf("Claude adapter reported a native mirror error")
		}
		if eventType == "assistant" {
			for _, raw := range contentBlocks(event["message"]) {
				if block, ok := raw.(map[string]any); ok &&
					stringField(block, "type") == "tool_use" {
					observation.ToolUses++
					if stringField(block, "name") != "Bash" {
						return observation, fmt.Errorf("Claude adapter used a non-shell tool")
					}
					claudeToolID = stringField(block, "id")
				}
			}
		}
		if eventType == "user" {
			for _, raw := range contentBlocks(event["message"]) {
				block, ok := raw.(map[string]any)
				if !ok || stringField(block, "type") != "tool_result" ||
					block["is_error"] == true {
					continue
				}
				if toolResultHasMarker(block) {
					if stringField(block, "tool_use_id") == claudeToolID &&
						claudeToolID != "" {
						observation.ToolResults++
						claudeToolCompleteAt = sequence
					}
				}
			}
		}
		if eventType == "result" {
			claudeResults++
			if event["is_error"] == true {
				return observation, fmt.Errorf("Claude adapter reported a provider error")
			}
			if event["is_error"] == false &&
				event["result"] == liveProviderTurnMarker {
				claudeFinals++
				claudeFinalAt = sequence
			}
		}
	}
	if sessionStarts != 1 {
		return observation, fmt.Errorf("provider adapter returned an ambiguous session-start count")
	}
	if harness == "codex" {
		if codexThreadStarts != 1 || codexTurnStarts != 1 ||
			codexTurnCompletes != 1 || codexThreadID == "" ||
			codexThreadID != observation.SessionID ||
			codexToolStarts != 1 || codexToolCompletes != 1 ||
			codexToolCompleteAt == 0 || codexFinalAt <= codexToolCompleteAt ||
			codexTurnCompleteAt <= codexFinalAt ||
			turnCompleteAt <= codexTurnCompleteAt {
			return observation, fmt.Errorf("Codex stream lacks one ordered native thread/tool/final sequence")
		}
	} else if !claudeDelta {
		return observation, fmt.Errorf("provider adapter returned no incremental stream delta")
	} else if observation.ProviderRequests == 0 {
		return observation, fmt.Errorf("provider adapter returned no native message start")
	} else if claudeInits != 1 || claudeResults != 1 || claudeInitID == "" ||
		claudeInitID != observation.SessionID ||
		claudeFinals != 1 || claudeToolCompleteAt == 0 ||
		claudeFinalAt <= claudeToolCompleteAt ||
		turnCompleteAt <= claudeFinalAt {
		return observation, fmt.Errorf("Claude stream lacks one ordered native init/tool/final sequence")
	}
	if turnCompletes != 1 {
		return observation, fmt.Errorf("provider adapter returned an ambiguous exact completion count")
	}
	if err := validLiveProviderObservation(observation); err != nil {
		return observation, err
	}
	return observation, nil
}
