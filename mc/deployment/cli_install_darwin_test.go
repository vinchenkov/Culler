package deployment

import "testing"

func TestCLIPathResolvesInActualNewMacShells(t *testing.T) {
	home, userHome, directory := cliInstallFixture(t)
	if _, err := InstallCLIPath(home, userHome, directory, "release", "/usr/bin:/bin"); err != nil {
		t.Fatal(err)
	}
}
