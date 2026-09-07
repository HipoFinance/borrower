package borrower

import (
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tvm/cell"
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

// A config-17 cell, so that GetMaxStakeFactor is read against the layout it claims rather than a
// hand-built slice of it.
func stakeConfigCell(minStake, maxStake, minTotalStake uint64, maxStakeFactor uint32) *cell.Cell {
	return cell.BeginCell().
		MustStoreBigCoins(new(big.Int).SetUint64(minStake)).
		MustStoreBigCoins(new(big.Int).SetUint64(maxStake)).
		MustStoreBigCoins(new(big.Int).SetUint64(minTotalStake)).
		MustStoreUInt(uint64(maxStakeFactor), 32).
		EndCell()
}

func TestGetMinStakeAndMaxStakeFactor(t *testing.T) {
	// mainnet values as of 2026-09-07
	c := stakeConfigCell(300_000e9, 10_000_000e9, 75_000_000e9, 294912)

	if got := GetMinStake(c); got.Cmp(new(big.Int).SetUint64(300_000e9)) != 0 {
		t.Fatalf("expected min_stake 300000 GRAM, got %v", got)
	}
	if got := GetMaxStakeFactor(c); got != 294912 {
		t.Fatalf("expected max_stake_factor 294912, got %d", got)
	}
	if got := FactorRatio(294912); got != 4.5 {
		t.Fatalf("expected ratio 4.5, got %v", got)
	}
}

// The whole point of the change: a configured ratio is a copy of a network parameter, so zero has
// to mean "ask the network", and a stale copy has to be visible rather than silent.
func TestResolveMaxFactor(t *testing.T) {
	const networkMax = uint32(294912) // 4.5

	// Not exactly representable as a float32, so the clamp must not fire on the rounding noise --
	// which is why the comparison happens on the 65536 scale and not on the ratios. Kept in a
	// variable so the expected factor is the same truncation the code performs, not a literal.
	inexact := float32(1.1)

	cases := []struct {
		name   string
		ratio  float32
		factor uint32
		note   string // substring the log line must contain, empty for no line at all
	}{
		{"zero follows the network", 0, networkMax, ""},
		{"stale value is honoured and reported", 3.0, 196608, "below the network maximum"},
		{"at the maximum, nothing to say", 4.5, networkMax, ""},
		{"above the maximum is clamped", 6.0, networkMax, "exceeds the network maximum"},
		{"inexact ratio below the maximum", inexact, uint32(inexact * 65536), "below the network maximum"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factor, note := resolveMaxFactor(tc.ratio, networkMax)
			if factor != tc.factor {
				t.Fatalf("expected max_factor %d, got %d", tc.factor, factor)
			}
			if tc.note == "" {
				if note != "" {
					t.Fatalf("expected no note, got %q", note)
				}
				return
			}
			if !strings.Contains(note, tc.note) {
				t.Fatalf("expected a note containing %q, got %q", tc.note, note)
			}
		})
	}
}

// Below the network maximum is a warning and not a refusal: a low factor earns less, it does not
// bid wrongly, and a validator that will not start earns nothing at all.
func TestResolveMaxFactorBelowMaximumStillBids(t *testing.T) {
	factor, note := resolveMaxFactor(3.0, 294912)
	if factor == 0 {
		t.Fatal("expected a usable max_factor so that a bid is still built")
	}
	if note == "" {
		t.Fatal("expected the stale value to be reported")
	}
}

func TestResolveMaxFactorRejectsRatiosBelowOne(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a ratio in (0, 1) to be refused")
		}
	}()
	resolveMaxFactor(0.5, 294912)
}

// The elector checks the signature over the confirmation against the fields of new_stake_msg, so
// the max_factor in the two has to be the same value -- the resolved one. This fails if resolution
// is ever moved downstream of the signing, which is the mistake that would cost a whole round.
func TestClampedMaxFactorReachesBothSignedCells(t *testing.T) {
	const networkMax = uint32(294912)
	const roundSince = uint32(1788759816)

	maxFactor, _ := resolveMaxFactor(6.0, networkMax) // configured above the maximum, so clamped
	if maxFactor != networkMax {
		t.Fatalf("expected the clamped factor %d, got %d", networkMax, maxFactor)
	}

	loanAddress := address.MustParseAddr("Ef_WCNvYfr9q1jsf4nDZdhPxEwDv9ONv9-Srlug4_fkmlGiC")
	adnl := new(big.Int).SetUint64(0x20791d63c50ed91d)
	publicKey := make([]byte, 32)
	signature := make([]byte, 64)

	confirmation := buildStakeConfirmation(roundSince, maxFactor, loanAddress, adnl).BeginParse()
	confirmation.MustLoadUInt(32) // 0x654c5074
	confirmation.MustLoadUInt(32) // round_since
	inConfirmation := uint32(confirmation.MustLoadUInt(32))

	newStakeMsg := buildNewStakeMsg(publicKey, roundSince, maxFactor, adnl, signature).BeginParse()
	newStakeMsg.MustLoadBigUInt(256) // validator public key
	newStakeMsg.MustLoadUInt(32)     // round_since
	inNewStakeMsg := uint32(newStakeMsg.MustLoadUInt(32))

	if inConfirmation != inNewStakeMsg {
		t.Fatalf("max_factor differs: confirmation %d, new_stake_msg %d", inConfirmation, inNewStakeMsg)
	}
	if inConfirmation != networkMax {
		t.Fatalf("expected the clamped %d in both cells, got %d", networkMax, inConfirmation)
	}
}
