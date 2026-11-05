package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestUpdateAtomicWithRecoveryRestoresUnrelatedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := Default()
	original.Surfaces.Discord = Discord{Enabled: true, BotToken: "private-test-token", GuildID: "12", ChannelID: "13", OperatorIDs: []string{"14"}}
	if _, err := WriteAtomic(path, original); err != nil {
		t.Fatal(err)
	}
	recovered := false
	err := UpdateAtomicWithRecovery(path, func(next *Config) error {
		next.Surfaces.Discord = Discord{OperatorIDs: []string{}}
		return nil
	}, func(next Config) error {
		stored, err := Load(path)
		if err != nil || stored.Surfaces.Discord.Enabled {
			t.Fatal("The desired configuration did not reach storage.")
		}
		return errors.New("The activation failed.")
	}, func(previous Config) error {
		recovered = true
		stored, err := Load(path)
		if err != nil || !stored.Surfaces.Discord.Enabled || stored.Surfaces.Discord.BotToken != original.Surfaces.Discord.BotToken {
			t.Fatal("The saved configuration was not restored.")
		}
		if stored.Timing != original.Timing {
			t.Fatal("Unrelated values changed.")
		}
		return nil
	})
	if err == nil || !recovered {
		t.Fatal("The failed activation received no recovery.")
	}
}
