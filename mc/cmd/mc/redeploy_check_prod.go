//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"io"
	operatorconfig "mc/config"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mc/deployment"
	"mc/substrate"
	"mc/verbs"
)

// redeployCheckMC is the container path of the Linux mc of the new release.
const redeployCheckMC = "/mc/check/mc"

// redeployCheckAttempts covers a database write that holds the write-ahead
// log while the read-only mount cannot open it.
const redeployCheckAttempts = 3

// redeployCheck is the state of the deployed database before redeploy.sh
// changes the deployment. State is new, absent, current, or differs.
type redeployCheck struct {
	State          string `json:"state"`
	DeploymentUUID string `json:"deployment_uuid,omitempty"`
}

// runRedeployCheck compares the deployed database with the schema of this
// release while the deployment runs. A container of the deployed image runs
// the Linux mc of this release with a read-only mount of the database volume.
// A usage error means that the check could not run.
func runRedeployCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || !filepath.IsAbs(args[1]) {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __redeploy-check <absolute path of the Linux mc>"))
	}
	identity, err := verbs.LoadIdentity()
	if err == nil {
		err = verbs.RequireHostScope(identity, "redeploy database check")
	}
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	info, err := os.Stat(args[1])
	if err != nil || !info.Mode().IsRegular() {
		return writeVerbError(stdout, stderr, verbs.Usagef("The Linux mc for the database check is not a regular file."))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("%v", err))
	}
	image, err := configuredProductionImage()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("%v", err))
	}
	result, err := checkRedeployDatabase(execDockerRunner{}, home, image, args[1], time.Sleep)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func checkRedeployDatabase(docker dockerCommandRunner, home, imageRef, linuxMC string, sleep func(time.Duration)) (redeployCheck, error) {
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return redeployCheck{}, verbs.Usagef("%v", err)
	}
	mirror, mirrored, err := readRedeployMirror(home)
	if err != nil {
		return redeployCheck{}, err
	}
	absent := redeployCheck{State: "new"}
	if mirrored {
		absent.State = "absent"
	}
	volume, exists, err := inspectJSON[helperVolumeInspect](docker, "volume", names.Volume)
	if err != nil {
		return redeployCheck{}, verbs.Usagef("The database volume check failed: %v", err)
	}
	if !exists {
		return absent, nil
	}
	if volume.Name != names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return redeployCheck{}, verbs.Domainf("The deployment database volume identity is invalid.")
	}
	image, exists, err := inspectJSON[helperImageInspect](docker, "image", imageRef)
	if err != nil || !exists || image.OS != "linux" || image.Architecture != "arm64" || !validContainerID(strings.TrimPrefix(image.ID, "sha256:")) {
		return redeployCheck{}, verbs.Usagef("The deployed image %s is unavailable for the database check.", imageRef)
	}
	// The mc in the image runs with the user identity that owns the database.
	// This mc runs directly, so the container takes that identity.
	args := []string{"run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--cap-drop", "ALL", "--user", "10001:10001",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + names.Volume + ",dst=/mc/spine,readonly",
		"--mount", "type=bind,src=" + linuxMC + ",dst=" + redeployCheckMC + ",readonly",
		"--entrypoint", redeployCheckMC, image.ID, "__database-schema"}
	var out []byte
	for attempt := 1; ; attempt++ {
		out, err = docker.CombinedOutput(args...)
		if err == nil {
			break
		}
		if attempt == redeployCheckAttempts {
			return redeployCheck{}, verbs.Usagef("The database check could not read the database: %s", setupDiagnosticAt(home, string(out)))
		}
		sleep(time.Second)
	}
	var found verbs.DeployedDatabase
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if dec.Decode(&found) != nil || found.ProtocolVersion != 1 || found.SchemaID != substrate.CurrentSchemaID {
		return redeployCheck{}, verbs.Usagef("The database check returned an invalid release identity.")
	}
	switch found.State {
	case "absent":
		return absent, nil
	case "current", "differs":
		if mirrored && found.DeploymentUUID != mirror {
			return redeployCheck{}, verbs.Domainf("The database and deployment identities differ.")
		}
		return redeployCheck{State: found.State, DeploymentUUID: found.DeploymentUUID}, nil
	}
	return redeployCheck{}, verbs.Usagef("The database check returned an invalid state.")
}

func readRedeployMirror(home string) (string, bool, error) {
	b, err := os.ReadFile(filepath.Join(home, "deployment.uuid"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, verbs.Usagef("read the deployment identity: %v", err)
	}
	uuid := strings.TrimSpace(string(b))
	if uuid == "" {
		return "", false, verbs.Domainf("The deployment identity is empty.")
	}
	return uuid, true, nil
}
