package verbs

import (
	"os"
	"path/filepath"
	"strconv"
)

// TaskArtifactBind is the read-only bind of the artifact files of one task in
// the Collie container of a packet conversation. Source is nil when the task
// folder is absent, for example after artifact expiry.
type TaskArtifactBind struct {
	Destination string  `json:"destination"`
	Source      *string `json:"source"`
}

// TaskArtifactMount resolves the bind of the artifact files of one task. The
// destination is the prefix of each artifact path that the task records, so
// each recorded path resolves in the container. The source is the canonical
// task folder in the Project artifact root.
func TaskArtifactMount(home, workspaceRoot string, task int64) (TaskArtifactBind, error) {
	roots, err := ArtifactHostRoots(home)
	if err != nil {
		return TaskArtifactBind{}, err
	}
	target, inside, err := projectArtifactPlane(roots, workspaceRoot)
	if err != nil {
		return TaskArtifactBind{}, err
	}
	name := strconv.FormatInt(task, 10)
	// The container root is a literal: a host override of the artifact root
	// must not move a container path.
	destination := "/mc/artifacts/" + target
	if inside != "" {
		destination += "/" + filepath.ToSlash(inside)
	}
	bind := TaskArtifactBind{Destination: destination + "/" + name}

	artifactRoot, err := filepath.EvalSymlinks(ProjectArtifactRoot(workspaceRoot))
	if err != nil {
		return TaskArtifactBind{}, Domainf("the Project artifact root %s is unreadable: %v", ProjectArtifactRoot(workspaceRoot), err)
	}
	folder := filepath.Join(artifactRoot, name)
	source, err := filepath.EvalSymlinks(folder)
	if os.IsNotExist(err) {
		return bind, nil
	}
	if err != nil {
		return TaskArtifactBind{}, Domainf("The artifact files of task %d are unreadable: %v", task, err)
	}
	// A task folder that resolves to another place could expose the files of
	// another task or of the host.
	if source != folder {
		return TaskArtifactBind{}, Domainf("The artifact files of task %d resolve outside the artifact root.", task)
	}
	info, err := os.Stat(source)
	if err != nil {
		return TaskArtifactBind{}, Domainf("The artifact files of task %d are unreadable: %v", task, err)
	}
	if !info.IsDir() {
		return TaskArtifactBind{}, Domainf("The artifact files of task %d are not a folder.", task)
	}
	bind.Source = &source
	return bind, nil
}
