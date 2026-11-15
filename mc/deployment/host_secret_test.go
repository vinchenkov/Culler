package deployment

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

type memoryHostSecretStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *memoryHostSecretStore) Read(service, account string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, exists := s.values[service+"/"+account]
	if !exists {
		return nil, fmt.Errorf("missing fixture")
	}
	return append([]byte(nil), body...), nil
}
func (s *memoryHostSecretStore) Write(service, account string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	key := service + "/" + account
	if _, exists := s.values[key]; exists {
		return fmt.Errorf("duplicate fixture")
	}
	s.values[key] = append([]byte(nil), body...)
	return nil
}
func (s *memoryHostSecretStore) Delete(service, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, service+"/"+account)
	return nil
}

func TestMain(m *testing.M) {
	// Ordinary tests cannot access the operator's Keychain.
	hostSecretBackend = &memoryHostSecretStore{}
	os.Exit(m.Run())
}
