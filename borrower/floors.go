package borrower

import (
	"fmt"
	"math/big"

	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
)

// AuctionFloors are the three floors request_loan enforces since the treasury's auction-floors release,
// all governor-set and 0 for off:
//
//   - MinEfficiency: a bid whose Efficiency is below it is refused and its collateral bounced.
//   - MinRequestStake: a request whose loan + collateral is below it is refused the same way.
//   - StakeCapFloor: a non-zero max_stake below it is not refused but raised to it, and stored raised.
//
// The two stakes are held here in nanoGRAM; the treasury stores and returns them in whole GRAM.
type AuctionFloors struct {
	MinEfficiency   uint64
	MinRequestStake *big.Int
	StakeCapFloor   *big.Int
}

// auctionFloorsIndex is where get_treasury_state returns the floors: min_efficiency, min_request_stake
// and stake_cap_floor at 28, 29 and 30, appended after total_request_fees.
const auctionFloorsIndex = 28

var oneGram = big.NewInt(1_000_000_000)

// loadAuctionFloors reads the floors from get_treasury_state's result, nil on a treasury that predates
// them. Such a treasury enforces none of them, so nil and all-zero floors behave the same.
func loadAuctionFloors(state *ton.ExecutionResult) *AuctionFloors {
	if len(state.AsTuple()) <= auctionFloorsIndex+2 {
		return nil
	}
	return &AuctionFloors{
		MinEfficiency:   state.MustInt(auctionFloorsIndex).Uint64(),
		MinRequestStake: new(big.Int).Mul(state.MustInt(auctionFloorsIndex+1), oneGram),
		StakeCapFloor:   new(big.Int).Mul(state.MustInt(auctionFloorsIndex+2), oneGram),
	}
}

// MinPaymentForEfficiency is the smallest min_payment whose Efficiency on this loan reaches e: the
// treasury rounds min_payment down to units of 2^30 nano, so it is a whole number of those units.
func MinPaymentForEfficiency(e uint64, loan *big.Int) *big.Int {
	units := new(big.Int).Rsh(loan, 40)
	if units.Sign() == 0 {
		units.SetInt64(1)
	}
	// ceil(e * units / 1000) units of 2^30
	payment := new(big.Int).Mul(new(big.Int).SetUint64(e), units)
	payment.Add(payment, big.NewInt(999))
	payment.Quo(payment, big.NewInt(1000))
	return payment.Lsh(payment, 30)
}

// FloorCap is the max_stake the treasury will store for a configured one: a non-zero cap below the
// stake_cap_floor is raised to it, as request_loan does, and anything else is unchanged. The borrower
// sends and compares the raised value, so a standing request reads as the one it sent instead of a
// mismatch that would be replaced, and charged a request fee, on every check.
func FloorCap(maxStake *big.Int, floors *AuctionFloors) (*big.Int, bool) {
	if floors == nil || maxStake.Sign() == 0 || floors.StakeCapFloor.Sign() == 0 ||
		maxStake.Cmp(floors.StakeCapFloor) >= 0 {
		return maxStake, false
	}
	return new(big.Int).Set(floors.StakeCapFloor), true
}

// CheckEfficiencyFloor is why the treasury would refuse this bid's rate, or "" when it would not.
func CheckEfficiencyFloor(minPayment, loan *big.Int, floors *AuctionFloors) string {
	if floors == nil || floors.MinEfficiency == 0 {
		return ""
	}
	e := Efficiency(minPayment, loan)
	if e >= floors.MinEfficiency {
		return ""
	}
	return fmt.Sprintf("borrow.min_payment of %v GRAM on a loan of %v GRAM is efficiency %d, below the "+
		"treasury's min_efficiency of %d; the treasury would refuse it. Raise borrow.min_payment to at least "+
		"%v GRAM for this loan", tlb.FromNanoTON(minPayment).String(), tlb.FromNanoTON(loan).String(), e,
		floors.MinEfficiency, tlb.FromNanoTON(MinPaymentForEfficiency(floors.MinEfficiency, loan)).String())
}

// CheckRequestStakeFloor is why the treasury would refuse this request's size, or "" when it would
// not. staked is the collateral the treasury will hold after the send, the request fee excluded.
func CheckRequestStakeFloor(loan, staked *big.Int, floors *AuctionFloors) string {
	if floors == nil || floors.MinRequestStake.Sign() == 0 {
		return ""
	}
	total := new(big.Int).Add(loan, staked)
	if total.Cmp(floors.MinRequestStake) >= 0 {
		return ""
	}
	return fmt.Sprintf("a loan of %v GRAM with %v GRAM of collateral is %v GRAM, below the treasury's "+
		"min_request_stake of %v GRAM; the treasury would refuse it. Raise borrow.loan (or borrow.stake) by "+
		"%v GRAM", tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(staked).String(),
		tlb.FromNanoTON(total).String(), tlb.FromNanoTON(floors.MinRequestStake).String(),
		tlb.FromNanoTON(new(big.Int).Sub(floors.MinRequestStake, total)).String())
}
