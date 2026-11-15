package main_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestVisionConversationResumesBeforeUpload(t *testing.T) {
	spine := initSpine(t)
	start := func() mcResult {
		t.Helper()
		result := runMC(t, spineEnv(spine), "", "collie", "start", "--from", "dashboard:vision-ws-test", "--vision-project", "ws-test")
		if result.code != 0 {
			t.Fatal(result.stderr)
		}
		return result
	}
	first := start()
	session := first.json["session_id"].(string)
	if result := runMC(t, spineEnv(spine), "", "collie", "end", session, "--reason", "The conversation is idle."); result.code != 0 {
		t.Fatal(result.stderr)
	}
	resumed := start()
	if resumed.json["session_id"] != session || resumed.json["status"] != "active" {
		t.Fatalf("resume = %+v", resumed.json)
	}
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	writeGreenAttachmentCanaryReceipt(t, spine, generation)
	holdAttachmentHealthGuard(t, spine, generation)
	request := map[string]any{
		"session_id": session, "request_id": "after-return", "body": "",
		"files": []any{map[string]any{"name": "goal.txt", "media_type": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("Help people record recipes."))}},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	uploaded := runMC(t, spineEnv(spine), string(body), "__dashboard-upload")
	if uploaded.code != 0 {
		t.Fatalf("upload after return: %s %s", uploaded.stdout, uploaded.stderr)
	}
	history := runMC(t, spineEnv(spine), "", "collie", "history", session)
	if history.code != 0 {
		t.Fatal(history.stderr)
	}
	messages := history.json["messages"].([]any)
	if len(messages) != 1 || len(messages[0].(map[string]any)["attachments"].([]any)) != 1 {
		t.Fatalf("history = %+v", history.json)
	}
}
