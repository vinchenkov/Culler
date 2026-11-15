package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

type EnvironmentFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type EnvironmentCommand struct {
	Command   string `json:"command"`
	Directory string `json:"directory"`
}

type EnvironmentService struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	Directory    string `json:"directory"`
	ReadyCommand string `json:"ready_command"`
}

type EnvironmentVariable struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	Reference string `json:"reference,omitempty"`
	Scope     string `json:"scope"`
}

type EnvironmentConfiguration struct {
	BaseDigest   string                `json:"base_digest"`
	Dockerfile   *EnvironmentFile      `json:"dockerfile"`
	BuildFiles   []EnvironmentFile     `json:"build_files"`
	BuildArgs    map[string]string     `json:"build_args"`
	Setup        EnvironmentCommand    `json:"setup"`
	Services     []EnvironmentService  `json:"services"`
	Variables    []EnvironmentVariable `json:"variables"`
	Secrets      []EnvironmentVariable `json:"secrets"`
	Declarations []string              `json:"declarations"`
	SourceCommit string                `json:"source_commit,omitempty"`
}

type EnvironmentRequest struct {
	ProjectID          string                    `json:"project_id,omitempty"`
	ExpectedRevisionID string                    `json:"expected_revision_id,omitempty"`
	RevisionID         string                    `json:"revision_id,omitempty"`
	OperationID        string                    `json:"operation_id,omitempty"`
	Configuration      *EnvironmentConfiguration `json:"configuration,omitempty"`
	Summary            string                    `json:"summary,omitempty"`
	Details            string                    `json:"details,omitempty"`
	Explanation        string                    `json:"explanation,omitempty"`
	Instruction        string                    `json:"instruction,omitempty"`
	DefaultImageDigest string                    `json:"default_image_digest,omitempty"`
	BaseDigest         string                    `json:"base_digest,omitempty"`
	CandidateSource    string                    `json:"candidate_source,omitempty"`
	RuntimeIdentity    string                    `json:"runtime_identity,omitempty"`
	InputsDigest       string                    `json:"inputs_digest,omitempty"`
	ImageDigest        string                    `json:"image_digest,omitempty"`
	Status             string                    `json:"status,omitempty"`
	Outcome            string                    `json:"outcome,omitempty"`
	Error              string                    `json:"error,omitempty"`
	RuntimeCompatible  bool                      `json:"runtime_compatible,omitempty"`
	Metadata           map[string]any            `json:"metadata,omitempty"`
	FilesDigest        string                    `json:"files_digest,omitempty"`
	SourceCommit       string                    `json:"source_commit,omitempty"`
	RunID              string                    `json:"run_id,omitempty"`
	ToolVersions       map[string]string         `json:"tool_versions,omitempty"`
	PackageRecords     []string                  `json:"package_records,omitempty"`
	RuntimeBinding     PrivateRuntimeBinding     `json:"runtime_binding,omitempty"`
}

type EnvironmentState struct {
	SetupStatus         string `json:"setup_status"`
	ProjectID           string `json:"project_id"`
	ProjectTitle        string `json:"project_title"`
	ProjectStatus       string `json:"project_status"`
	SetupID             string `json:"setup_id"`
	VisionRevision      string `json:"vision_revision"`
	SelectedBaseDigest  string `json:"selected_base_digest"`
	SelectedRevisionID  string `json:"selected_revision_id"`
	SelectedImageDigest string `json:"selected_image_digest"`
	DraftRevisionID     string `json:"draft_revision_id"`
	Readiness           string `json:"readiness"`
	Phase               string `json:"phase"`
	Cause               string `json:"cause"`
	ActiveOperationID   string `json:"active_operation_id"`
	Authorization       string `json:"authorization"`
	Instruction         string `json:"instruction"`
	RuntimeCondition    string `json:"runtime_condition"`
	ImageMissing        bool   `json:"image_missing"`
	AssociationPending  bool   `json:"association_pending"`
}

type EnvironmentRevision struct {
	ID            string                   `json:"id"`
	ProjectID     string                   `json:"project_id"`
	Editor        string                   `json:"editor"`
	Configuration EnvironmentConfiguration `json:"configuration"`
	InputsDigest  string                   `json:"inputs_digest"`
	Explanation   string                   `json:"explanation"`
	CreatedAt     string                   `json:"created_at"`
}

type EnvironmentOperation struct {
	NativeSessionRef string                   `json:"native_session_ref,omitempty"`
	TraceFilename    string                   `json:"trace_filename,omitempty"`
	Trace            any                      `json:"trace"`
	ID               string                   `json:"id"`
	ProjectID        string                   `json:"project_id"`
	RevisionID       string                   `json:"revision_id"`
	Kind             string                   `json:"kind"`
	Status           string                   `json:"status"`
	InputsDigest     string                   `json:"inputs_digest"`
	ImageDigest      string                   `json:"image_digest"`
	RuntimeIdentity  string                   `json:"runtime_identity"`
	RuntimeBinding   PrivateRuntimeBinding    `json:"runtime_binding"`
	Attempt          int                      `json:"attempt"`
	Error            string                   `json:"error"`
	CreatedAt        string                   `json:"created_at"`
	EndedAt          string                   `json:"ended_at"`
	WorkspaceRoot    string                   `json:"workspace_root,omitempty"`
	Configuration    EnvironmentConfiguration `json:"configuration"`
	Brief            string                   `json:"brief,omitempty"`
	Skills           []map[string]any         `json:"skills,omitempty"`
	Metadata         map[string]any           `json:"metadata,omitempty"`
	ReportDigest     string                   `json:"-"`
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var environmentID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var imageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var environmentRegistryDigest = regexp.MustCompile(`^[a-zA-Z0-9._:/-]+@sha256:[a-f0-9]{64}$`)
var commitDigest = regexp.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`)

func environmentHash(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func environmentBuildDigest(c EnvironmentConfiguration) string {
	return environmentHash(struct {
		Base       string
		Dockerfile *EnvironmentFile
		Files      []EnvironmentFile
		Args       map[string]string
	}{c.BaseDigest, c.Dockerfile, c.BuildFiles, c.BuildArgs})
}

func environmentBaseReference(digest string) string {
	return "culler-project-base:" + digest[strings.LastIndex(digest, "sha256:")+7:]
}

func environmentRelative(value string, directory bool) bool {
	if directory && (value == "" || value == ".") {
		return true
	}
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, "\\\x00\r\n") || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == ".git" || part == "" {
			return false
		}
	}
	return true
}

func reservedEnvironmentName(name string) bool {
	for _, prefix := range []string{"MC_", "CULLER_", "LD_", "DYLD_", "BASH_FUNC_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "BASH_ENV", "ENV", "NODE_OPTIONS", "BUN_OPTIONS", "PYTHONPATH", "PYTHONHOME", "GCONV_PATH", "LOCPATH", "GLIBC_TUNABLES":
		return true
	}
	return false
}

func ValidateEnvironmentConfiguration(c *EnvironmentConfiguration) error {
	if c == nil || !imageDigest.MatchString(c.BaseDigest) {
		return Usagef("The environment requires an exact base image digest.")
	}
	if c.SourceCommit != "" && !commitDigest.MatchString(c.SourceCommit) {
		return Usagef("The source commit is invalid.")
	}
	if c.BuildFiles == nil {
		c.BuildFiles = []EnvironmentFile{}
	}
	if c.BuildArgs == nil {
		c.BuildArgs = map[string]string{}
	}
	if c.Services == nil {
		c.Services = []EnvironmentService{}
	}
	if c.Variables == nil {
		c.Variables = []EnvironmentVariable{}
	}
	if c.Secrets == nil {
		c.Secrets = []EnvironmentVariable{}
	}
	if c.Declarations == nil {
		c.Declarations = []string{}
	}
	if c.Setup.Directory == "" {
		c.Setup.Directory = "."
	}
	if !environmentRelative(c.Setup.Directory, true) || strings.ContainsRune(c.Setup.Command, 0) {
		return Usagef("The setup directory or command is invalid.")
	}
	files := map[string]bool{}
	total := 0
	for _, f := range c.BuildFiles {
		if !environmentRelative(f.Path, false) || files[f.Path] || strings.ContainsRune(f.Content, 0) {
			return Usagef("Each build file requires a unique relative path and text contents.")
		}
		files[f.Path] = true
		total += len(f.Content)
	}
	sort.Slice(c.BuildFiles, func(i, j int) bool { return c.BuildFiles[i].Path < c.BuildFiles[j].Path })
	if c.Dockerfile != nil {
		if !environmentRelative(c.Dockerfile.Path, false) || files[c.Dockerfile.Path] {
			return Usagef("The Dockerfile requires a unique relative path.")
		}
		total += len(c.Dockerfile.Content)
		if err := validateEnvironmentDockerfile(*c); err != nil {
			return err
		}
	} else if len(c.BuildFiles) > 0 || len(c.BuildArgs) > 0 {
		return Usagef("Build files and arguments require a Dockerfile.")
	}
	if total > 2*1024*1024 || len(c.BuildFiles) > 128 {
		return Usagef("The retained build contents exceed the size limit.")
	}
	for name, value := range c.BuildArgs {
		if !environmentName.MatchString(name) || reservedEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return Usagef("A build argument is invalid.")
		}
	}
	services := map[string]bool{}
	for i := range c.Services {
		v := &c.Services[i]
		if v.Directory == "" {
			v.Directory = "."
		}
		if !environmentID.MatchString(v.Name) || services[v.Name] || !environmentRelative(v.Directory, true) || v.Command == "" || v.ReadyCommand == "" {
			return Usagef("Each service requires a name, command, directory, and readiness command.")
		}
		services[v.Name] = true
	}
	names := map[string]bool{}
	for group, entries := range [][]EnvironmentVariable{c.Variables, c.Secrets} {
		for _, v := range entries {
			if !environmentName.MatchString(v.Name) || reservedEnvironmentName(v.Name) || names[v.Name] || (v.Scope != "setup" && v.Scope != "agent" && v.Scope != "both") {
				return Usagef("Environment values require unique permitted names and explicit scopes.")
			}
			if group == 1 && (!environmentID.MatchString(v.Reference) || v.Value != "") {
				return Usagef("A secret requires a local reference without a secret value.")
			}
			if group == 0 && (v.Reference != "" || strings.ContainsRune(v.Value, 0)) {
				return Usagef("A nonsecret variable contains an invalid value or secret reference.")
			}
			names[v.Name] = true
		}
	}
	for _, file := range c.Declarations {
		if !environmentRelative(file, false) {
			return Usagef("A declaration requires a relative repository path.")
		}
	}
	return nil
}

func validateEnvironmentDockerfile(c EnvironmentConfiguration) error {
	body := strings.ReplaceAll(c.Dockerfile.Content, "\\\n", " ")
	from := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		verb := strings.ToUpper(fields[0])
		if verb == "FROM" {
			from++
			if len(fields) != 2 || fields[1] != environmentBaseReference(c.BaseDigest) {
				return Usagef("The Dockerfile must extend %s.", environmentBaseReference(c.BaseDigest))
			}
		}
		if from == 0 {
			return Usagef("The Dockerfile must start with the selected base.")
		}
		if verb == "ADD" || verb == "ONBUILD" || verb == "VOLUME" || verb == "ENTRYPOINT" || verb == "CMD" || verb == "HEALTHCHECK" {
			return Usagef("The Dockerfile instruction %s is unsupported for Project additions.", verb)
		}
		if strings.Contains(line, "--mount=") || strings.Contains(line, "--security=") || strings.Contains(line, "--network=host") {
			return Usagef("The Dockerfile requests an unsupported build capability.")
		}
	}
	if from != 1 {
		return Usagef("The Dockerfile requires one selected base.")
	}
	return nil
}
