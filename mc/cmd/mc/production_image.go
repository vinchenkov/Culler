package main

import (
	"os"
	"path/filepath"
	"strings"

	"fmt"
	"mc/deployment"
	"mc/distribution"
)

// productionImageRef names the agent image that a source install builds.
const productionImageRef = "mc-prod"

// configuredProductionImage returns the agent image of the deployment.
// A published install uses the image digest of its release record.
func configuredProductionImage() (string, error) {
	if image, err := testProviderImage(); image != "" || err != nil {
		return image, err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return "", err
	}
	path := os.Getenv("CULLER_RELEASE_RECORD")
	if path == "" {
		if pending, err := distribution.Load(filepath.Join(home, "state", "pending-release.json")); err == nil && pending.Commit == releaseBuildID {
			return pending.Image, nil
		}
		path = filepath.Join(home, distribution.InstalledRecord)
	}
	r, err := distribution.Load(path)
	if os.IsNotExist(err) && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return productionImageRef, nil
	}
	if err != nil {
		return "", err
	}
	// Local source deployments retain their separate image identity.
	if r.Commit != releaseBuildID && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return productionImageRef, nil
	}
	return strings.TrimSpace(r.Image), nil
}

// configuredProjectImage selects the prepared default for new Projects only.
func configuredProjectImage() (string, error) {
	home, err := configuredCanonicalHome()
	if err != nil {
		return "", err
	}
	path := os.Getenv("CULLER_RELEASE_RECORD")
	if path == "" {
		if pending, err := distribution.Load(filepath.Join(home, "state", "pending-release.json")); err == nil && pending.Commit == releaseBuildID {
			if pending.ProjectImage == "" {
				return "", fmt.Errorf("the release has no default Project image")
			}
			return pending.ProjectImage, nil
		}
		path = filepath.Join(home, distribution.InstalledRecord)
	}
	record, err := distribution.Load(path)
	if os.IsNotExist(err) && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return deployment.DefaultProjectImage, nil
	}
	if err != nil {
		return "", err
	}
	if record.Commit != releaseBuildID && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return deployment.DefaultProjectImage, nil
	}
	if record.ProjectImage == "" {
		return "", fmt.Errorf("the release has no default Project image")
	}
	return record.ProjectImage, nil
}
