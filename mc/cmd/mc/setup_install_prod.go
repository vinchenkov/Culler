//go:build !test_fake_routing

package main

import (
	"errors"
	"io"
	"mc/deployment"
	"mc/verbs"
	"os"
	"path/filepath"
)

func installOnboardCLI(private bool, args verbs.OnboardArgs) (bool, error) {
	if !private || args.HostReleaseSource == "" || os.Getenv("MC_BIN_DIR") == "" {
		return false, nil
	}
	_, err := installSetupExecutable()
	return err == nil, err
}

func installSetupExecutable() (path string, retErr error) {
	defer func() {
		if retErr != nil {
			home, _ := configuredCanonicalHome()
			retErr = &setupFailure{cause: setupSafeDiagnostic(retErr.Error()), command: cullerInstallCommand(home)}
		}
	}()
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	directory := os.Getenv("MC_BIN_DIR")
	if directory == "" {
		return source, nil
	}
	if !filepath.IsAbs(directory) {
		return "", errors.New("The executable directory requires an absolute path.")
	}
	target := filepath.Join(directory, "mc")
	if err := deployment.CheckCLIConflict(filepath.Join(directory, "culler")); err != nil {
		return "", err
	}
	if source == target {
		return target, installCullerCommand(source, directory)
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return "", err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.CreateTemp(directory, ".mc-install-")
	if err != nil {
		return "", err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return "", err
	}
	if err := output.Chmod(0755); err != nil {
		return "", err
	}
	if err := output.Sync(); err != nil {
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(output.Name(), target); err != nil {
		return "", err
	}
	return target, installCullerCommand(target, directory)
}

func installCullerCommand(source, directory string) error {
	if err := copyPublishedFile(source, filepath.Join(directory, "culler"), 0755); err != nil {
		return err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	parentPath, ok := os.LookupEnv("CULLER_PARENT_PATH")
	if !ok {
		parentPath = os.Getenv("PATH")
	}
	_, err = deployment.InstallCLIPath(home, userHome, directory, releaseBuildID, parentPath)
	return err
}
