package borrower

import (
	"math/big"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type ParticipationState uint8

const (
	ParticipationOpen ParticipationState = iota
	ParticipationDistributing
	ParticipationStaked
	ParticipationValidating
	ParticipationHeld
	ParticipationRecovering
	ParticipationBurning
)

func (s ParticipationState) String() string {
	switch s {
	case ParticipationOpen:
		return "open"
	case ParticipationDistributing:
		return "distributing"
	case ParticipationStaked:
		return "staked"
	case ParticipationValidating:
		return "validating"
	case ParticipationHeld:
		return "held"
	case ParticipationRecovering:
		return "recovering"
	case ParticipationBurning:
		return "burning"
	}
	return "unknown"
}

type Participation struct {
	State           ParticipationState
	Size            uint16
	Sorted          *cell.Dictionary
	Requests        *cell.Dictionary
	Rejected        *cell.Dictionary
	Accepted        *cell.Dictionary
	Accrued         *cell.Dictionary
	Staked          *cell.Dictionary
	Recovering      *cell.Dictionary
	TotalStaked     *big.Int
	TotalRecovered  *big.Int
	CurrentVsetHash *big.Int
	StakeHeldFor    uint32
	StakeHeldUntil  uint32
}

func LoadParticipation(c *cell.Cell) Participation {
	s := c.BeginParse()
	return Participation{
		State:           ParticipationState(s.MustLoadUInt(4)),
		Size:            uint16(s.MustLoadUInt(16)),
		Sorted:          s.MustLoadDict(112),
		Requests:        s.MustLoadDict(256),
		Rejected:        s.MustLoadDict(256),
		Accepted:        s.MustLoadDict(256),
		Accrued:         s.MustLoadDict(256),
		Staked:          s.MustLoadDict(256),
		Recovering:      s.MustLoadDict(256),
		TotalStaked:     s.MustLoadBigCoins(),
		TotalRecovered:  s.MustLoadBigCoins(),
		CurrentVsetHash: s.MustLoadBigUInt(256),
		StakeHeldFor:    uint32(s.MustLoadUInt(32)),
		StakeHeldUntil:  uint32(s.MustLoadUInt(32)),
	}
}
