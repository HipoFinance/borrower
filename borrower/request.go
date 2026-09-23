package borrower

import (
	"math/big"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type Request struct {
	MinPayment *big.Int
	// Out of 65535. A bid made on the old 0-255 scale is exactly this value divided by 257.
	RewardShare  uint16
	LoanAmount   *big.Int
	AccrueAmount *big.Int
	StakeAmount  *big.Int
	// The borrower fee in force when the request was made, out of 65535 of the contractual share of
	// the round reward. The treasury snapshots it here rather than reading it at recovery, so a
	// governance change cannot reprice a loan that is already committed.
	BorrowerFee uint16
	NewStakeMsg *cell.Cell
}

func LoadRequest(c *cell.Cell) Request {
	s := c.MustBeginParse()
	return Request{
		MinPayment:   s.MustLoadBigCoins(),
		RewardShare:  uint16(s.MustLoadUInt(16)),
		LoanAmount:   s.MustLoadBigCoins(),
		AccrueAmount: s.MustLoadBigCoins(),
		StakeAmount:  s.MustLoadBigCoins(),
		BorrowerFee:  uint16(s.MustLoadUInt(16)),
		NewStakeMsg:  s.MustLoadRef().MustToCell(),
	}
}

// OpRequestLoan is op::request_loan in the contract's contracts/imports/constants.fc.
const OpRequestLoan = 0x36335da9

// RequestValue is what a request_loan message must carry. The treasury keeps
// value - request fee as collateral and refuses the request unless that covers
// min_payment + max_punishment, plus fee::min_burn while a borrower fee is in force; any own stake
// rides on top. The punishment is floored at 1 GRAM, as this borrower always has.
func RequestValue(maxPunishment, requestLoanFee, minPayment, stake *big.Int, borrowerFee uint16) *big.Int {
	value := big.NewInt(1000000000)
	if maxPunishment.Cmp(value) == 1 {
		value.Set(maxPunishment)
	}
	value.Add(value, requestLoanFee)
	value.Add(value, minPayment)
	value.Add(value, stake)
	if borrowerFee != 0 {
		// Collateral, not a fee: whatever the burn does not take comes back with loan_result.
		value.Add(value, minBurn)
	}
	return value
}

// RequestBody builds the request_loan body. It carries no reward share: the treasury sets one for
// every loan, and its end_parse refuses a body that still has 16 bits of share before the ref.
func RequestBody(queryID uint64, roundSince uint32, loan, minPayment *big.Int, newStakeMsg *cell.Cell) *cell.Cell {
	return cell.BeginCell().
		MustStoreUInt(OpRequestLoan, 32).
		MustStoreUInt(queryID, 64).
		MustStoreUInt(uint64(roundSince), 32).
		MustStoreBigCoins(loan).
		MustStoreBigCoins(minPayment).
		MustStoreRef(newStakeMsg).
		EndCell()
}

// Unchanged reports whether a request already standing in the treasury is the one this borrower
// would send, so that it is not replaced -- each replacement costs another request fee. The share is
// compared too: a request keeps the share it was made under, so one made before the governor changed
// it is re-sent to carry the current value.
func (r Request) Unchanged(loan, minPayment *big.Int, rewardShare uint16) bool {
	return r.MinPayment.Cmp(minPayment) == 0 && r.RewardShare == rewardShare && r.LoanAmount.Cmp(loan) == 0
}
