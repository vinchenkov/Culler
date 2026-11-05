//go:build !darwin && !linux

package onboarding

import "fmt"

func LoadSession(string) (Session, error) {
	return Session{}, fmt.Errorf("setup records require macOS or Linux")
}

func SaveSession(string, Session) error {
	return fmt.Errorf("setup records require macOS or Linux")
}
