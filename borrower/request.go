package borrower

import (
	"math/big"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type Request struct {
	MinPayment           *big.Int
	ValidatorRewardShare uint16
	LoanAmount           *big.Int
	AccrueAmount         *big.Int
	StakeAmount          *big.Int
	NewStakeMsg          *cell.Cell
}

func LoadRequest(c *cell.Cell) Request {
	s := c.BeginParse()
	return Request{
		MinPayment:           s.MustLoadBigCoins(),
		ValidatorRewardShare: uint16(s.MustLoadUInt(14)),
		LoanAmount:           s.MustLoadBigCoins(),
		AccrueAmount:         s.MustLoadBigCoins(),
		StakeAmount:          s.MustLoadBigCoins(),
		NewStakeMsg:          s.MustLoadRef().MustToCell(),
	}
}
