package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type hostSecretReference struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Size      int    `json:"size"`
}

func hostSecretReferencePath(home, kind, reference string) string {
	return filepath.Join(home, "host-credential-references", kind+"-"+reference+".json")
}

func retainHostSecretReference(home, kind, reference string, size int) error {
	root := filepath.Join(home, "host-credential-references")
	if err := ensureOwnerDirectory(root, "host credential references"); err != nil {
		return err
	}
	body, _ := json.Marshal(hostSecretReference{Version: 1, Kind: kind, Reference: reference, Size: size})
	file, err := os.OpenFile(hostSecretReferencePath(home, kind, reference), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncDirectory(root)
	}
	return err
}

func deleteHostSecretReference(home, kind, reference string) error {
	path := hostSecretReferencePath(home, kind, reference)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		return nil
	}
	return syncDirectory(filepath.Dir(path))
}

// DeleteInstallationHostSecrets runs only after services and containers stop.
// The reference files remain available until each protected deletion succeeds.
func DeleteInstallationHostSecrets(home string) error {
	references := map[string]hostSecretReference{}
	root := filepath.Join(home, "host-credential-references")
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		body, err := readSecretSource(filepath.Join(root, entry.Name()), "host credential reference")
		if err != nil {
			return err
		}
		var ref hostSecretReference
		if decodeRuntimeAuthJSON(body, &ref) != nil || ref.Version != 1 || !validSHA256(ref.Reference) || ref.Size < 1 || ref.Size > maxHostSecretBytes || entry.Name() != ref.Kind+"-"+ref.Reference+".json" {
			return fmt.Errorf("The host credential reference is invalid.")
		}
		references[ref.Kind+":"+ref.Reference] = ref
	}
	// References from earlier installations still identify their protected values.
	entries, err = os.ReadDir(filepath.Join(home, "refresh-grants"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := readSecretSource(filepath.Join(home, "refresh-grants", entry.Name()), "connection credential reference")
		if err != nil {
			return err
		}
		var ref providerCredentialReference
		if json.Unmarshal(body, &ref) != nil || ref.CredentialRef == "" {
			continue
		}
		if !validSHA256(ref.CredentialRef) {
			return fmt.Errorf("The connection credential reference is invalid.")
		}
		key := "provider:" + ref.CredentialRef
		if _, exists := references[key]; !exists {
			references[key] = hostSecretReference{Kind: "provider", Reference: ref.CredentialRef}
		}
	}
	body, err := os.ReadFile(filepath.Join(home, "provider-proxy", "tls.json"))
	if err == nil {
		var ref providerTLSReference
		if json.Unmarshal(body, &ref) != nil || !validSHA256(ref.Reference) {
			return fmt.Errorf("The proxy certificate reference is invalid.")
		}
		key := "proxy-tls:" + ref.Reference
		if _, exists := references[key]; !exists {
			references[key] = hostSecretReference{Kind: "proxy-tls", Reference: ref.Reference}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, ref := range references {
		if err := DeleteHostSecret(home, ref.Kind, ref.Reference); err != nil {
			return err
		}
	}
	return nil
}

func deleteReplacedProviderReferences(home, previous, current string) {
	entries, err := os.ReadDir(previous)
	if err != nil {
		return
	}
	for _, entry := range entries {
		before, err := os.ReadFile(filepath.Join(previous, entry.Name()))
		if err != nil {
			continue
		}
		var old providerCredentialReference
		if json.Unmarshal(before, &old) != nil || old.CredentialRef == "" {
			continue
		}
		after, _ := os.ReadFile(filepath.Join(current, entry.Name()))
		var next providerCredentialReference
		_ = json.Unmarshal(after, &next)
		if old.CredentialRef != next.CredentialRef {
			_ = DeleteHostSecret(home, "provider", old.CredentialRef)
		}
	}
}
