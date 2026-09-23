package borrower

import (
	"math/big"
	"testing"

	"github.com/xssnick/tonutils-go/tlb"
)

func gram(t *testing.T, s string) *big.Int {
	t.Helper()
	c, err := tlb.FromTON(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.Nano()
}

// Requests made on mainnet in September 2026, with the efficiency the treasury ranked them at.
func TestEfficiencyMatchesTheTreasury(t *testing.T) {
	// Exact nano amounts: the treasury rounds from these, so a figure rounded to two decimals can land
	// one unit of efficiency away.
	cases := []struct {
		loan, minPayment string
		want             uint64
	}{
		{"1514175.735590550", "1109.175304192", 750},
		{"865153.278240499", "541.165879296", 641},
		{"1014275.328079896", "571.230650368", 577},
		{"400222", "195.421", 498},
	}
	for _, c := range cases {
		got := Efficiency(gram(t, c.minPayment), gram(t, c.loan))
		if got != c.want {
			t.Errorf("Efficiency(%v on %v) = %v, want %v", c.minPayment, c.loan, got, c.want)
		}
	}
}

func TestEfficiencyOfATinyLoanDoesNotDivideByZero(t *testing.T) {
	if got := Efficiency(gram(t, "10"), gram(t, "1")); got == 0 {
		t.Errorf("a loan under one rounding unit should still rank, got %v", got)
	}
}

func TestBidRate(t *testing.T) {
	got := BidRate(gram(t, "651"), gram(t, "1000000"))
	want := "651.00 GRAM per 1,000,000 staked (efficiency 666)"
	if got != want {
		t.Errorf("BidRate = %q, want %q", got, want)
	}
}
