package deployment

import (
	"context"
	"fmt"
	"strings"
)

// ImageRemoval lists the image references that one removal removed and kept.
type ImageRemoval struct {
	Removed  []string    `json:"removed"`
	Kept     []KeptImage `json:"kept"`
	Problems []string    `json:"problems,omitempty"`
}

// KeptImage names an image reference that stays and the reason.
type KeptImage struct {
	Reference string `json:"reference"`
	Reason    string `json:"reason"`
}

const (
	imageKeptNeeded   = "a Project or the installed release needs the image"
	imageKeptInUse    = "a container uses the image"
	imageKeptNoDigest = "the image has no registry digest"
)

// ProjectBaseReference names the base tag of a Project image by its image ID.
func ProjectBaseReference(imageID string) string {
	return "culler-project-base:" + strings.TrimPrefix(imageID, "sha256:")
}

type listedImage struct {
	Reference string
	Digest    string
	ID        string
}

// RemoveSupersededImages removes the previous product image and each default
// Project image of projectRepository that no Project needs. The needed list
// holds image references and image IDs that stay. The removal uses references
// and never image IDs, so a layer that another image shares stays. Docker
// refuses the removal of an image that a container uses; that image stays and
// the result names the reason. The log receives one line for each event.
func RemoveSupersededImages(ctx context.Context, previousImage, projectRepository string, needed []string, run RuntimeDocker, log func(string)) ImageRemoval {
	if run == nil {
		run = runRuntimeDocker
	}
	if log == nil {
		log = func(string) {}
	}
	result := ImageRemoval{Removed: []string{}, Kept: []KeptImage{}}
	if previousImage != "" {
		result.remove(ctx, previousImage, run, log)
	}
	if projectRepository == "" {
		return result
	}
	images, err := listProjectImages(ctx, projectRepository, run)
	if err != nil {
		log(fmt.Sprintf("The default Project images stay: %v", err))
		result.Problems = append(result.Problems, err.Error())
		return result
	}
	keep := map[string]bool{}
	for _, value := range needed {
		keep[value] = true
	}
	for _, image := range images {
		switch {
		case keep[image.Reference] || keep[image.Digest] || keep[image.ID]:
			result.keep(image.Reference, imageKeptNeeded, log)
		case image.Digest == "":
			result.keep(image.Reference, imageKeptNoDigest, log)
		default:
			result.removeProjectImage(ctx, image, run, log)
		}
	}
	return result
}

// listProjectImages lists each image of the repository with its registry
// digest and its image ID. An image without a registry digest has an empty
// Digest.
func listProjectImages(ctx context.Context, repository string, run RuntimeDocker) ([]listedImage, error) {
	out, err := run(ctx, "", "image", "ls", "--digests", "--no-trunc", "--format", `{{.Repository}}@{{.Digest}}\t{{.ID}}`, repository)
	if err != nil {
		return nil, fmt.Errorf("the image list of %s failed: %s", repository, dockerMessage(out, err))
	}
	var images []listedImage
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		reference, id, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || seen[reference] {
			continue
		}
		seen[reference] = true
		image := listedImage{Reference: reference, ID: strings.TrimSpace(id)}
		if _, digest, _ := strings.Cut(reference, "@"); strings.HasPrefix(digest, "sha256:") {
			image.Digest = digest
		}
		images = append(images, image)
	}
	return images, nil
}

func (r *ImageRemoval) removeProjectImage(ctx context.Context, image listedImage, run RuntimeDocker, log func(string)) {
	base := ProjectBaseReference(image.ID)
	out, err := run(ctx, "", "image", "inspect", "--format", "{{.Id}}", base)
	if err == nil && strings.TrimSpace(string(out)) == image.ID {
		if out, err := run(ctx, "", "image", "rm", base); err != nil {
			r.keep(image.Reference, removalReason(out, err), log)
			return
		}
		r.Removed = append(r.Removed, base)
		log("Removed image " + base + ".")
	}
	r.remove(ctx, image.Reference, run, log)
}

func (r *ImageRemoval) remove(ctx context.Context, reference string, run RuntimeDocker, log func(string)) {
	out, err := run(ctx, "", "image", "rm", reference)
	if err != nil && strings.Contains(strings.ToLower(string(out)), "no such image") {
		log("The image " + reference + " is absent.")
		return
	}
	if err != nil {
		r.keep(reference, removalReason(out, err), log)
		return
	}
	r.Removed = append(r.Removed, reference)
	log("Removed image " + reference + ".")
}

func (r *ImageRemoval) keep(reference, reason string, log func(string)) {
	r.Kept = append(r.Kept, KeptImage{Reference: reference, Reason: reason})
	if reason != imageKeptNeeded && reason != imageKeptInUse && reason != imageKeptNoDigest {
		r.Problems = append(r.Problems, reason)
	}
	log("Kept image " + reference + ": " + reason + ".")
}

// removalReason reads the Docker message of a refused removal.
func removalReason(out []byte, err error) string {
	message := strings.ToLower(string(out))
	if strings.Contains(message, "is using its referenced image") || strings.Contains(message, "image is being used by") {
		return imageKeptInUse
	}
	return "the removal failed: " + dockerMessage(out, err)
}

// dockerMessage selects the first line of the Docker output, or the error.
func dockerMessage(out []byte, err error) string {
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}
