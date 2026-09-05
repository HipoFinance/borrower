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
	s := c.BeginParse()
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
