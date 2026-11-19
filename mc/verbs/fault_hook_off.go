//go:build !test_cli_faults

package verbs

// faultPoint is a no-op in every build except the test-only tag below; no
// production configuration can arm it because the armed file is not compiled.
func faultPoint(string) error { return nil }
