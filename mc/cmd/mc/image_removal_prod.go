//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"mc/deployment"
)

// imageRemovalRecord is the image part of the operation record of an update.
type imageRemovalRecord struct {
	deployment.ImageRemoval
	Log []string `json:"log"`
}

// removeSupersededImages removes the previous product image and each default
// Project image that no Project needs, then keeps the result for the
// operation record. An image that Docker refuses to remove stays.
func (p *publishedSetup) removeSupersededImages(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	record := &imageRemovalRecord{Log: []string{}}
	log := func(line string) { record.Log = append(record.Log, line) }
	previous := ""
	if p.installed.Image != "" && p.installed.Image != p.record.Image {
		previous = p.installed.Image
	}
	repository, _, _ := strings.Cut(p.record.ProjectImage, "@")
	needed := []string{p.record.ProjectImage}
	var problems []string
	digests, err := p.projectImageDigests(ctx)
	if err != nil {
		log("The default Project images stay: the selected Project images are unknown: " + setupSafeDiagnostic(err.Error()))
		repository = ""
		problems = append(problems, "The selected Project images are unknown: "+setupSafeDiagnostic(err.Error()))
	}
	run := func(ctx context.Context, _ string, args ...string) ([]byte, error) { return p.docker(ctx, args...) }
	record.ImageRemoval = deployment.RemoveSupersededImages(ctx, previous, repository, append(needed, digests...), run, log)
	record.Problems = append(record.Problems, problems...)
	p.images = record
}

// projectImageDigests reads the image digests that Projects select. The
// digests exist only in the spine, so the helper container reads them.
func (p *publishedSetup) projectImageDigests(ctx context.Context) ([]string, error) {
	names, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return nil, err
	}
	out, err := p.docker(ctx, "exec", names.Helper, "mc", "__project-image-digests")
	if err != nil {
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return nil, errors.New(line)
			}
		}
		return nil, err
	}
	var payload struct {
		Digests []string `json:"digests"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return nil, errors.New("the helper returned an invalid digest list")
	}
	return payload.Digests, nil
}
