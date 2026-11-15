package main_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mc/verbs"
)

func TestEnvironmentReportRequiresImageDependencyApproval(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	defer db.Close()
	_, err := verbs.ProjectEnvironment(db, nil, "assist", verbs.EnvironmentRequest{
		ProjectID: "ws-test", Instruction: "Check the Project prerequisites.",
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := verbs.ProjectEnvironment(db, nil, "work", verbs.EnvironmentRequest{
		DefaultImageDigest: "sha256:" + strings.Repeat("a", 64), RuntimeIdentity: "approval-fixture",
		RuntimeBinding: verbs.PrivateRuntimeBinding{ID: "fake", Harness: "fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := work.(map[string]any)["operations"].([]verbs.EnvironmentOperation)
	if len(ops) != 1 || ops[0].Kind != "setup" {
		t.Fatalf("The fixture has unexpected operations: %+v", ops)
	}
	op := ops[0]
	configuration := op.Configuration
	configuration.Dockerfile = &verbs.EnvironmentFile{
		Path: ".culler/Dockerfile", Content: "FROM culler-project-base:" + strings.Repeat("a", 64) + "\nRUN touch /usr/local/share/image-dependency-proof\n",
	}
	cases := []struct {
		name    string
		request verbs.EnvironmentRequest
		code    int
		message string
	}{
		{"image approval", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The Project preparation is complete.", Configuration: &configuration}, 1, "Image dependencies require a proposal and operator approval."},
		{"outcome", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "blocked", Summary: "The compiler is unavailable."}, 2, "The setup outcome must be proposal or complete."},
		{"summary", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete"}, 2, "The setup summary requires one paragraph with at most two short sentences."},
		{"summary length", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: strings.Repeat("word ", 26)}, 2, "Each setup summary sentence permits at most 25 words."},
		{"summary bytes", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: strings.Repeat("x", 1025)}, 2, "The setup summary permits at most 1024 bytes."},
		{"details bytes", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "The preparation is complete.", Details: strings.Repeat("x", 24001)}, 2, "The setup details permit at most 24000 bytes."},
		{"report fields", verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Explanation: "The tools are unavailable."}, 2, "Use summary and details for the setup report."},
	}
	for _, scenario := range cases {
		body, err := json.Marshal(scenario.request)
		if err != nil {
			t.Fatal(err)
		}
		for _, columns := range []int{80, 40} {
			for _, color := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s_columns_%d_color_%t", scenario.name, columns, color), func(t *testing.T) {
					env := append(runJSONEnv(t, spine, op.ID, "setup", "setup"), fmt.Sprintf("COLUMNS=%d", columns), "TERM=xterm-256color")
					if color {
						env = append(env, "CLICOLOR_FORCE=1", "NO_COLOR=")
					} else {
						env = append(env, "NO_COLOR=1", "CLICOLOR=0")
					}
					result := runMC(t, env, string(body), "environment", "report")
					if result.code != scenario.code || result.stderr != "mc: "+scenario.message+"\n" {
						t.Fatalf("The report received an unexpected result: %d %s %s", result.code, result.stdout, result.stderr)
					}
					envelope, ok := result.json["error"].(map[string]any)
					if !ok || envelope["message"] != scenario.message {
						t.Fatalf("The refusal envelope differs from the terminal output: %s", result.stdout)
					}
					t.Logf("Terminal output:\n%s", result.stderr)
					var rows []string
					for line := strings.TrimSuffix(result.stderr, "\n"); line != ""; {
						end := min(columns, len(line))
						rows = append(rows, line[:end])
						line = line[end:]
					}
					t.Logf("Terminal rows at %d columns:\n%s", columns, strings.Join(rows, "\n"))
				})
			}
		}
	}
	if got := queryStr(t, db, `SELECT status FROM environment_operations WHERE id=?`, op.ID); got != "queued" {
		t.Fatalf("The refused report changed the operation state to %q.", got)
	}
	if got := queryStr(t, db, `SELECT COALESCE(report_digest,'') FROM environment_operations WHERE id=?`, op.ID); got != "" {
		t.Fatal("The refused report stored a successful setup result.")
	}
}

func TestEnvironmentReportInitialProposalRequiresImageChange(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	defer db.Close()
	for _, statement := range []string{
		`DELETE FROM project_environments WHERE project_id='ws-test'`,
		`UPDATE projects SET status='paused',setup_status='configured' WHERE id='ws-test'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := verbs.ProjectEnvironment(db, nil, "assist", verbs.EnvironmentRequest{ProjectID: "ws-test", Instruction: "Prepare the Project."}); err != nil {
		t.Fatal(err)
	}
	work, err := verbs.ProjectEnvironment(db, nil, "work", verbs.EnvironmentRequest{
		DefaultImageDigest: "sha256:" + strings.Repeat("a", 64), RuntimeIdentity: "initial-fixture",
		RuntimeBinding: verbs.PrivateRuntimeBinding{ID: "fake", Harness: "fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	op := work.(map[string]any)["operations"].([]verbs.EnvironmentOperation)[0]
	configuration := op.Configuration
	configuration.Setup = verbs.EnvironmentCommand{Directory: ".", Command: "npm ci"}
	body, err := json.Marshal(verbs.EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Summary: "The setup script installs the Project dependencies.", Configuration: &configuration})
	if err != nil {
		t.Fatal(err)
	}
	const message = "An initial proposal must change the image inputs. Submit other configuration changes in a complete report."
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			t.Run(fmt.Sprintf("columns_%d_color_%t", columns, color), func(t *testing.T) {
				env := append(runJSONEnv(t, spine, op.ID, "setup", "setup"), fmt.Sprintf("COLUMNS=%d", columns), "TERM=xterm-256color")
				if color {
					env = append(env, "CLICOLOR_FORCE=1", "NO_COLOR=")
				} else {
					env = append(env, "NO_COLOR=1", "CLICOLOR=0")
				}
				result := runMC(t, env, string(body), "environment", "report")
				if result.code != 1 || result.stderr != "mc: "+message+"\n" {
					t.Fatalf("The report received an unexpected result: %d %s %s", result.code, result.stdout, result.stderr)
				}
				t.Logf("Terminal output:\n%s", result.stderr)
			})
		}
	}
	if got := queryStr(t, db, `SELECT phase FROM project_environments WHERE project_id='ws-test'`); got == "approval" {
		t.Fatal("A setup script proposal requested operator approval.")
	}
}
