package borrower

import (
	"math/big"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Everything in this file is pure. It decides whether a standing request is outranked and what
// min_payment answers the outbid, from the same formulas the treasury ranks on: request_sort_key in
// the contract's contracts/imports/utils.fc, and the order decide_loan_requests serves requests in.

const (
	minPaymentShift = 30
	loanAmountShift = 40
	efficiencyBits  = 24
	shareBits       = 16
	loanCompBits    = 80
)

func loanAmountRound(loan *big.Int) *big.Int {
	lar := new(big.Int).Rsh(loan, loanAmountShift)
	if lar.Sign() == 0 {
		return big.NewInt(1)
	}
	return lar
}

// SortKey reproduces request_sort_key exactly. Higher sorts first. The treasury decides in key
// descending order, and on an equal key the lower address first.
func SortKey(minPayment, loan *big.Int, rewardShare uint16) *big.Int {
	key := new(big.Int).Lsh(new(big.Int).SetUint64(Efficiency(minPayment, loan)), shareBits+loanCompBits)
	treasuryShare := big.NewInt(int64(65535 - rewardShare))
	key.Or(key, new(big.Int).Lsh(treasuryShare, loanCompBits))
	comp := new(big.Int).Lsh(big.NewInt(1), loanCompBits)
	comp.Sub(comp, loanAmountRound(loan))
	return key.Or(key, comp)
}

// MinPaymentFor is the smallest min_payment that reaches the efficiency at this loan. The result is
// on a 2^30 boundary, because anything above one and below the next ranks the same.
func MinPaymentFor(efficiency uint64, loan *big.Int) *big.Int {
	mpr := new(big.Int).Mul(new(big.Int).SetUint64(efficiency), loanAmountRound(loan))
	mpr.Add(mpr, big.NewInt(999))
	mpr.Quo(mpr, big.NewInt(1000))
	return mpr.Lsh(mpr, minPaymentShift)
}

// RankedRequest is a standing request reduced to what ranks it.
type RankedRequest struct {
	Address    *big.Int
	Efficiency uint64
	Key        *big.Int
}

// RankOf ranks a request as the treasury stores it, by the validator key it is stored under.
func RankOf(address *big.Int, r Request) RankedRequest {
	return RankedRequest{
		Address:    address,
		Efficiency: Efficiency(r.MinPayment, r.LoanAmount),
		Key:        SortKey(r.MinPayment, r.LoanAmount, r.RewardShare),
	}
}

// Outranked returns the requests decided before ours: a higher key, or an equal key and a lower
// address. Our own request, found by address, is never one of them.
func Outranked(ourKey, ourAddress *big.Int, requests []RankedRequest) []RankedRequest {
	var out []RankedRequest
	for _, r := range requests {
		switch c := r.Key.Cmp(ourKey); {
		case r.Address.Cmp(ourAddress) == 0:
		case c > 0:
			out = append(out, r)
		case c == 0 && r.Address.Cmp(ourAddress) < 0:
			out = append(out, r)
		}
	}
	return out
}

// Answer is the min_payment that outranks the highest outranking request the ceiling can beat, or
// nil when there is none. ceiling is c = e(max_min_payment, loan). a = 1 + max{ e_r : e_r + 1 <= c },
// and a > e_r outranks that request whatever its loan size. current is our standing min_payment, which
// an answer never lowers.
func Answer(current, loan *big.Int, ceiling uint64, outranking []RankedRequest) *big.Int {
	var best uint64
	found := false
	for _, r := range outranking {
		if r.Efficiency+1 <= ceiling && (!found || r.Efficiency > best) {
			best = r.Efficiency
			found = true
		}
	}
	if !found {
		return nil
	}
	p := MinPaymentFor(best+1, loan)
	if p.Cmp(current) <= 0 {
		return nil
	}
	return p
}

// requestLoanMinPayment reads the min_payment of a request_loan body to the treasury for this round,
// and reports false for any other body.
func requestLoanMinPayment(body *cell.Cell, round uint32) (*big.Int, bool) {
	if body == nil {
		return nil, false
	}
	s, err := body.BeginParse()
	if err != nil {
		return nil, false
	}
	op, err := s.LoadUInt(32)
	if err != nil || op != OpRequestLoan {
		return nil, false
	}
	if _, err = s.LoadUInt(64); err != nil {
		return nil, false
	}
	since, err := s.LoadUInt(32)
	if err != nil || uint32(since) != round {
		return nil, false
	}
	if _, err = s.LoadBigCoins(); err != nil { // loan
		return nil, false
	}
	minPayment, err := s.LoadBigCoins()
	if err != nil {
		return nil, false
	}
	return minPayment, true
}

// countAnswers counts the sends, oldest first, whose min_payment is higher than the previous send's
// for the round. The first send is not an answer.
func countAnswers(minPayments []*big.Int) int {
	n := 0
	for i := 1; i < len(minPayments); i++ {
		if minPayments[i].Cmp(minPayments[i-1]) > 0 {
			n++
		}
	}
	return n
}
