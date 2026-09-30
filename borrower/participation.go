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
	// ParticipationReadyToBurn is a round that has settled and booked its reward but still holds its
	// bills while an older round can still book a reward.
	ParticipationReadyToBurn
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
	case ParticipationReadyToBurn:
		return "ready_to_burn"
	case ParticipationBurning:
		return "burning"
	}
	return "unknown"
}

type Participation struct {
	State    ParticipationState
	Size     uint16
	Sorted   *cell.Dictionary
	Requests *cell.Dictionary
	Rejected *cell.Cell
	// Rejected, Accepted and Accrued are internal to the treasury's loan decision: its working state,
	// held only between the messages of one decide chain (rejected requests are refunded in the same
	// chain), and keyed however that needs (accepted is 416 bits since the auction-floors release). They
	// are kept as raw cells and never read; a decided loan is in Staked once its stake is sent, moments
	// later.
	Accepted        *cell.Cell
	Accrued         *cell.Cell
	Staked          *cell.Dictionary
	Recovering      *cell.Dictionary
	TotalStaked     *big.Int
	TotalRecovered  *big.Int
	CurrentVsetHash *big.Int
	StakeHeldFor    uint32
	StakeHeldUntil  uint32
}

func LoadParticipation(c *cell.Cell) Participation {
	s := c.MustBeginParse()
	return Participation{
		State:           ParticipationState(s.MustLoadUInt(4)),
		Size:            uint16(s.MustLoadUInt(16)),
		Sorted:          s.MustLoadDict(120), // request_sort_key is 120 bits
		Requests:        s.MustLoadDict(256),
		Rejected:        loadInternalDict(s),
		Accepted:        loadInternalDict(s),
		Accrued:         loadInternalDict(s),
		Staked:          s.MustLoadDict(256),
		Recovering:      s.MustLoadDict(256),
		TotalStaked:     s.MustLoadBigCoins(),
		TotalRecovered:  s.MustLoadBigCoins(),
		CurrentVsetHash: s.MustLoadBigUInt(256),
		StakeHeldFor:    uint32(s.MustLoadUInt(32)),
		StakeHeldUntil:  uint32(s.MustLoadUInt(32)),
	}
}

// loadInternalDict steps over a dictionary internal to the treasury's loan decision, keeping its raw cell
// without parsing it.
func loadInternalDict(s *cell.Slice) *cell.Cell {
	if r := s.MustLoadMaybeRef(); r != nil {
		return r.MustToCell()
	}
	return nil
}
