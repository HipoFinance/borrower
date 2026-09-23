package borrower

import (
	"fmt"
	"math/big"
)

// Efficiency is the number the treasury ranks loan requests on: request_sort_key in the contract's
// contracts/imports/utils.fc. min_payment is rounded down to units of 2^30 nano (about 1.07 GRAM),
// the loan to units of 2^40 nano (about 1100 GRAM), and the result is capped at 24 bits. Requests are
// served in descending efficiency; on a tie the smaller loan goes first.
func Efficiency(minPayment, loan *big.Int) uint64 {
	payment := new(big.Int).Rsh(minPayment, 30)
	units := new(big.Int).Rsh(loan, 40)
	if units.Sign() == 0 {
		units.SetInt64(1)
	}
	e := new(big.Int).Mul(payment, big.NewInt(1000))
	e.Quo(e, units)
	if max := big.NewInt(1<<24 - 1); e.Cmp(max) > 0 {
		return max.Uint64()
	}
	return e.Uint64()
}

// BidRate describes a bid as what it costs per million GRAM staked. The treasury scales min_payment
// to everything a loan stakes, including any leftover it adds to the loan, so this rate -- not the
// min_payment alone -- is what the bid actually promises.
func BidRate(minPayment, loan *big.Int) string {
	if loan.Sign() == 0 {
		return "no loan"
	}
	perMillion := new(big.Rat).SetFrac(
		new(big.Int).Mul(minPayment, big.NewInt(1_000_000)),
		loan,
	)
	return fmt.Sprintf("%s GRAM per 1,000,000 staked (efficiency %d)",
		perMillion.FloatString(2), Efficiency(minPayment, loan))
}
