package borrower

import (
	"math/big"
	"testing"
)

var testLoan = gram(&testing.T{}, "1100000") // 1,000 units of 2^40 nano, near enough

func rr(addr int64, e uint64, key int64) RankedRequest {
	return RankedRequest{Address: big.NewInt(addr), Efficiency: e, Key: big.NewInt(key)}
}

// The smallest P for an efficiency, on a 2^30 boundary, and nothing smaller reaches it.
func TestMinPaymentForIsTheSmallestReaching(t *testing.T) {
	for _, loan := range []*big.Int{gram(t, "1100000"), gram(t, "700000"), gram(t, "1")} {
		for _, e := range []uint64{1, 600, 635, 1000, 1001, 5000} {
			p := MinPaymentFor(e, loan)
			if got := Efficiency(p, loan); got < e {
				t.Errorf("loan %v e %v: P %v reaches only %v", loan, e, p, got)
			}
			if new(big.Int).Lsh(new(big.Int).Rsh(p, 30), 30).Cmp(p) != 0 {
				t.Errorf("P %v is not on a 2^30 boundary", p)
			}
			if p.Sign() > 0 {
				below := new(big.Int).Sub(p, big.NewInt(1<<30))
				if Efficiency(below, loan) >= e {
					t.Errorf("loan %v e %v: %v is already enough, %v is not the smallest", loan, e, below, p)
				}
			}
		}
	}
}

func TestAnswer(t *testing.T) {
	loan := gram(t, "1100000")
	current := MinPaymentFor(600, loan)
	ceiling := Efficiency(MinPaymentFor(700, loan), loan) // c
	cases := []struct {
		name string
		rs   []RankedRequest
		want uint64 // 0: nothing
	}{
		{"no outranking request", nil, 0},
		{"beatable", []RankedRequest{rr(1, 650, 1)}, 651},
		{"at the ceiling", []RankedRequest{rr(1, ceiling, 1)}, 0},
		{"above the ceiling", []RankedRequest{rr(1, ceiling+5, 1)}, 0},
		{"one below the ceiling", []RankedRequest{rr(1, ceiling-1, 1)}, ceiling},
		{"one beatable and one not", []RankedRequest{rr(1, ceiling+50, 1), rr(2, 640, 1), rr(3, 620, 1)}, 641},
		{"equal efficiency, smaller loan", []RankedRequest{rr(1, 600, 1)}, 601},
	}
	for _, c := range cases {
		p := Answer(current, loan, ceiling, c.rs)
		if c.want == 0 {
			if p != nil {
				t.Errorf("%s: answered %v, want nothing", c.name, p)
			}
			continue
		}
		if p == nil {
			t.Errorf("%s: no answer, want efficiency %v", c.name, c.want)
			continue
		}
		if p.Cmp(MinPaymentFor(c.want, loan)) != 0 {
			t.Errorf("%s: P %v, want the smallest for %v", c.name, p, c.want)
		}
		if p.Cmp(MinPaymentFor(700, loan)) > 0 || Efficiency(p, loan) > ceiling {
			t.Errorf("%s: answer %v exceeds the ceiling", c.name, p)
		}
	}
}

// A ceiling equal to the starting bid never answers: nothing that outranks it has e_r + 1 <= c.
func TestCeilingAtTheStartingBidNeverAnswers(t *testing.T) {
	loan := gram(t, "1100000")
	p := MinPaymentFor(600, loan)
	c := Efficiency(p, loan)
	for e := c; e < c+20; e++ {
		if a := Answer(p, loan, c, []RankedRequest{rr(1, e, 1)}); a != nil {
			t.Errorf("answered a request at %v with ceiling %v", e, c)
		}
	}
}

func TestAnswerOutranksTheRivalWhateverItsLoan(t *testing.T) {
	loan := gram(t, "1100000")
	ours := SortKey(MinPaymentFor(600, loan), loan, 100)
	smaller := gram(t, "700000")
	rivalKey := SortKey(MinPaymentFor(600, smaller), smaller, 100)
	if rivalKey.Cmp(ours) <= 0 {
		t.Fatal("test setup: the smaller loan at equal efficiency should outrank")
	}
	e := Efficiency(MinPaymentFor(600, smaller), smaller)
	p := Answer(MinPaymentFor(600, loan), loan, e+10, []RankedRequest{{Address: big.NewInt(1), Efficiency: e, Key: rivalKey}})
	if p == nil || SortKey(p, loan, 100).Cmp(rivalKey) <= 0 {
		t.Errorf("answer %v does not outrank the rival", p)
	}
}

func TestOutranked(t *testing.T) {
	ours := big.NewInt(100)
	our := big.NewInt(50)
	rs := []RankedRequest{
		rr(10, 0, 101), // higher key
		rr(10, 0, 99),  // lower key
		rr(40, 0, 100), // equal key, lower address
		rr(60, 0, 100), // equal key, higher address
		rr(50, 0, 500), // ourselves
	}
	got := Outranked(ours, our, rs)
	if len(got) != 2 || got[0].Key.Int64() != 101 || got[1].Address.Int64() != 40 {
		t.Errorf("outranking = %+v, want the higher key and the equal key with the lower address", got)
	}
}

func TestSortKeyMatchesTheTreasuryLayout(t *testing.T) {
	loan := gram(t, "1100000")
	p := MinPaymentFor(635, loan)
	key := SortKey(p, loan, 2056)
	if got := new(big.Int).Rsh(key, 96).Uint64(); got != Efficiency(p, loan) {
		t.Errorf("efficiency field %v, want %v", got, Efficiency(p, loan))
	}
	if got := new(big.Int).And(new(big.Int).Rsh(key, 80), big.NewInt(0xffff)).Int64(); got != 65535-2056 {
		t.Errorf("treasury share field %v", got)
	}
	comp := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 80), loanAmountRound(loan))
	if got := new(big.Int).And(key, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 80), big.NewInt(1))); got.Cmp(comp) != 0 {
		t.Errorf("loan complement %v, want %v", got, comp)
	}
}

func TestCountAnswersCountsOnlyRises(t *testing.T) {
	n := func(v ...int64) []*big.Int {
		var out []*big.Int
		for _, x := range v {
			out = append(out, big.NewInt(x))
		}
		return out
	}
	for _, c := range []struct {
		in   []*big.Int
		want int
	}{{nil, 0}, {n(5), 0}, {n(5, 5), 0}, {n(5, 6, 6, 9), 2}, {n(9, 5), 0}} {
		if got := countAnswers(c.in); got != c.want {
			t.Errorf("countAnswers(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRequestLoanMinPaymentReadsOnlyThisRound(t *testing.T) {
	body := RequestBody(7, 1000, gram(t, "700000"), gram(t, "650"), big.NewInt(0), newEmptyCell())
	if p, ok := requestLoanMinPayment(body, 1000); !ok || p.Cmp(gram(t, "650")) != 0 {
		t.Errorf("got %v %v", p, ok)
	}
	if _, ok := requestLoanMinPayment(body, 1001); ok {
		t.Error("read a request for another round")
	}
}
