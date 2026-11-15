package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	root := os.Args[1]
	must(os.MkdirAll(root, 0700))
	path := filepath.Join(root, "trivy.db")
	must(os.RemoveAll(path))
	db, err := bolt.Open(path, 0600, nil)
	must(err)
	defer db.Close()
	must(db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"vulnerability", "data-source", "alpine 3.23"} {
			_, err := tx.CreateBucketIfNotExists([]byte(name))
			must(err)
		}
		if os.Args[2] == "advisory" {
			bucket := tx.Bucket([]byte("alpine 3.23"))
			pkg, err := bucket.CreateBucketIfNotExists([]byte("example"))
			must(err)
			must(pkg.Put([]byte("CVE-2099-1234"), []byte(`{"FixedVersion":"1.1.0-r0"}`)))
			must(tx.Bucket([]byte("vulnerability")).Put([]byte("CVE-2099-1234"), []byte(`{"Title":"Controlled advisory","Severity":"HIGH","VendorSeverity":{"alpine":3}}`)))
			must(tx.Bucket([]byte("data-source")).Put([]byte("alpine 3.23"), []byte(`{"ID":"alpine","Name":"Controlled advisory data","URL":"https://example.invalid/advisories"}`)))
		}
		return nil
	}))
	now := time.Now().UTC()
	body, err := json.Marshal(map[string]any{"Version": 2, "UpdatedAt": now, "DownloadedAt": now, "NextUpdate": now.Add(time.Hour)})
	must(err)
	must(os.WriteFile(filepath.Join(root, "metadata.json"), body, 0600))
}
