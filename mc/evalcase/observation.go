package evalcase

import (
	"fmt"
	"os"
	"path/filepath"
)

// ObservationArtifacts binds a later source comparison to one assessment revision.
func (s Store) ObservationArtifacts(id, observation string, identity Identity) ([]Artifact, error) {
	dir, err := s.Dir(id)
	if err != nil {
		return nil, err
	}
	store := Store{Root: filepath.Join(dir, "observations")}
	m, err := store.Load(observation)
	if err != nil {
		return nil, err
	}
	original := m.Identity
	original.Commit = identity.Commit
	if original != identity {
		return nil, fmt.Errorf("The source observation identifies another packet.")
	}
	if err = store.Validate(m); err != nil {
		return nil, err
	}
	if err = VerifySource(filepath.Join(store.Root, observation), m); err != nil {
		return nil, err
	}
	prefix := "observations/" + observation + "/"
	out := []Artifact{}
	for _, a := range m.Artifacts {
		a.Path = prefix + a.Path
		out = append(out, a)
	}
	body, err := os.ReadFile(filepath.Join(store.Root, observation, "manifest.json"))
	if err != nil {
		return nil, err
	}
	out = append(out, Artifact{Path: prefix + "manifest.json", Size: int64(len(body)), SHA256: Hash(body), Origin: "displayed source comparison", CapturedAt: Now(), MediaType: "application/json"})
	return out, nil
}
