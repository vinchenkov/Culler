//go:build !darwin

package deployment

type unavailableHostSecretStore struct{}

func platformHostSecretStore() hostSecretStore { return unavailableHostSecretStore{} }
func (unavailableHostSecretStore) Read(string, string) ([]byte, error) {
	return nil, ErrHostCredentialUnavailable
}
func (unavailableHostSecretStore) Write(string, string, []byte) error {
	return ErrHostCredentialUnavailable
}
func (unavailableHostSecretStore) Delete(string, string) error { return ErrHostCredentialUnavailable }
