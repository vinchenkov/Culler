package verbs

import "os"

// syncDir fsyncs a directory so a rename or file landed inside it is durable
// before its caller reports success. Callers that need a domain-specific
// failure wording wrap the returned error.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return Domainf("open directory for sync: %v", err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return Domainf("sync directory: %v", err)
	}
	return nil
}
