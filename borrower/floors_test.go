package borrower

import (
	"math/big"
	"strings"
	"testing"

	"github.com/xssnick/tonutils-go/ton"
)

// The floors the treasury's auction-floors release was deployed with on 2026-09-29.
func mainnetFloors(t *testing.T) *AuctionFloors {
	return &AuctionFloors{MinEfficiency: 620, MinRequestStake: gram(t, "680000"), StakeCapFloor: gram(t, "2500000")}
}

// A get_treasury_state result of n values, the last three being the floors when n reaches 31.
func treasuryStateOf(n int) *ton.ExecutionResult {
	values := make([]any, n)
	for i := range values {
		values[i] = big.NewInt(0)
	}
	if n > auctionFloorsIndex+2 {
		values[auctionFloorsIndex] = big.NewInt(620)
		values[auctionFloorsIndex+1] = big.NewInt(680000)
		values[auctionFloorsIndex+2] = big.NewInt(2500000)
	}
	return ton.NewExecutionResult(values)
}

func TestFloorsAreReadInGramAndAbsentOnAnOlderTreasury(t *testing.T) {
	f := loadAuctionFloors(treasuryStateOf(31))
	if f == nil || f.MinEfficiency != 620 || f.MinRequestStake.Cmp(gram(t, "680000")) != 0 ||
		f.StakeCapFloor.Cmp(gram(t, "2500000")) != 0 {
		t.Fatalf("floors = %+v, want 620, 680,000 GRAM and 2,500,000 GRAM in nano", f)
	}
	if f := loadAuctionFloors(treasuryStateOf(28)); f != nil {
		t.Fatalf("a 28-value tuple has no floors, got %+v", f)
	}
}

// The smallest min_payment that reaches an efficiency reaches it, and one unit of 2^30 nano less does not.
func TestMinPaymentForEfficiencyIsTheSmallestThatReachesIt(t *testing.T) {
	for _, loan := range []string{"300000", "800000", "1000000", "1460000", "1723060.440981462", "3000000"} {
		for _, e := range []uint64{1, 499, 620, 659, 677, 1000} {
			mp := MinPaymentForEfficiency(e, gram(t, loan))
			if got := Efficiency(mp, gram(t, loan)); got < e {
				t.Fatalf("loan %v, efficiency %d: min_payment %v reaches only %d", loan, e, mp, got)
			}
			less := new(big.Int).Sub(mp, big.NewInt(1<<30))
			if less.Sign() >= 0 && Efficiency(less, gram(t, loan)) >= e {
				t.Fatalf("loan %v, efficiency %d: %v is not the smallest min_payment", loan, e, mp)
			}
		}
	}
}

// The sample borrower.yaml's "0" defaults: min_payment 0, and a loan of the network's min_stake.
func TestTheSampleConfigIsStoppedBeforeItSends(t *testing.T) {
	floors := mainnetFloors(t)
	why := CheckEfficiencyFloor(big.NewInt(0), gram(t, "300000"), floors)
	if !strings.Contains(why, "efficiency 0, below the treasury's min_efficiency of 620") ||
		!strings.Contains(why, "Raise borrow.min_payment to at least 181.462368256 GRAM") {
		t.Fatalf("efficiency floor: %q", why)
	}
	why = CheckRequestStakeFloor(gram(t, "300000"), gram(t, "102"), floors)
	if !strings.Contains(why, "below the treasury's min_request_stake of 680000 GRAM") ||
		!strings.Contains(why, "by 379898 GRAM") {
		t.Fatalf("stake floor: %q", why)
	}
}

// A mainnet bid of 2026-09-29 clears all three floors, and floors that are off or absent refuse nothing.
func TestABidAboveTheFloorsIsLeftAlone(t *testing.T) {
	loan, mp, staked := gram(t, "1723060.440981462"), gram(t, "1109.175304192"), gram(t, "1210")
	for _, floors := range []*AuctionFloors{mainnetFloors(t), nil,
		{MinEfficiency: 0, MinRequestStake: big.NewInt(0), StakeCapFloor: big.NewInt(0)}} {
		if why := CheckEfficiencyFloor(mp, loan, floors); why != "" {
			t.Fatalf("efficiency 659 refused under %+v: %v", floors, why)
		}
		if why := CheckRequestStakeFloor(loan, staked, floors); why != "" {
			t.Fatalf("1.72M refused under %+v: %v", floors, why)
		}
	}
	if why := CheckEfficiencyFloor(big.NewInt(0), loan, nil); why != "" {
		t.Fatalf("a treasury without floors refuses nothing, got %v", why)
	}
}

// request_loan raises a non-zero cap below the floor and stores it raised; the borrower sends the same
// value, so the standing request compares equal instead of being replaced on every check.
func TestACapBelowTheFloorIsRaisedToIt(t *testing.T) {
	floors := mainnetFloors(t)
	cases := []struct {
		cap, want string
		raised    bool
	}{
		{"0", "0", false}, // no cap stays no cap
		{"1001500", "2500000", true},
		{"2499999.999999999", "2500000", true},
		{"2500000", "2500000", false},
		{"3071562.698", "3071562.698", false},
	}
	for _, c := range cases {
		got, raised := FloorCap(gram(t, c.cap), floors)
		if got.Cmp(gram(t, c.want)) != 0 || raised != c.raised {
			t.Fatalf("FloorCap(%v) = %v, %v; want %v, %v", c.cap, got, raised, c.want, c.raised)
		}
		standing := Request{MinPayment: big.NewInt(1), LoanAmount: big.NewInt(1), MaxStake: gram(t, c.want)}
		if !standing.Unchanged(big.NewInt(1), big.NewInt(1), 0, got) {
			t.Fatalf("a request standing with the stored cap %v reads as changed against %v", c.want, got)
		}
	}
	if got, raised := FloorCap(gram(t, "1001500"), nil); raised || got.Cmp(gram(t, "1001500")) != 0 {
		t.Fatalf("no floors, no raise: got %v", got)
	}
}
