package borrower

import (
	"os"
	"testing"
)

// The migration guard is the only thing standing between an unmigrated config and a validator that
// silently hands the pool almost its entire share, so it is worth an actual test.
func TestRewardShareMigrationGuard(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		refused bool
		share   uint16
	}{
		{"migrated", "borrow:\n    active: yes\n    reward_share: 2056\n", false, 2056},
		{"old key still present", "borrow:\n    active: yes\n    validator_reward_share: 8\n", true, 0},
		{"missing while borrowing", "borrow:\n    active: yes\n", true, 0},
		{"missing while inactive", "borrow:\n    active: no\n", false, 0},
	}

	original := ConfigFile
	defer func() { ConfigFile = original }()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "cfg*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tc.body); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			ConfigFile = f.Name()
			config, err := ReadConfig()
			if tc.refused {
				if err == nil {
					t.Fatalf("expected the config to be refused, got reward_share=%d", config.Borrow.RewardShare)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected the config to be accepted, got %v", err)
			}
			if config.Borrow.RewardShare != tc.share {
				t.Fatalf("expected reward_share %d, got %d", tc.share, config.Borrow.RewardShare)
			}
		})
	}
}
