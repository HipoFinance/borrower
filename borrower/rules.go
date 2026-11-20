package borrower

import (
	"errors"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// AuctionClose is what the treasury's get_auction_close answers: status 0 (no participation yet, E = B,
// a first bid is accepted until D), 1 (open, E = max(B, S)), 2 (nobody may send).
type AuctionClose struct {
	Status           int
	Close            uint32 // E
	BaseClose        uint32 // B
	ParticipateSince uint32 // D
}

const (
	lastSendMargin = 60 * time.Second
	expiryMargin   = 5 * time.Second
	messageTTL     = 180 * time.Second
	extensionZone  = 15 * time.Minute

	// exitMethodNotFound is the exit code of a getter the contract does not have.
	exitMethodNotFound = 11
)

// roundState is what is remembered about one round, in memory only.
type roundState struct {
	round   uint32
	answers int
	counted bool
	// lastAnswer is the min_payment of the last answer sent; sending it again is a re-send, not a new answer.
	lastAnswer *big.Int
}

// recordAnswer counts an answer about to be sent. A re-send of the answer last sent is not a further one.
func (s *roundState) recordAnswer(p *big.Int) {
	if s.lastAnswer == nil || s.lastAnswer.Cmp(p) != 0 {
		s.answers++
	}
	s.lastAnswer = new(big.Int).Set(p)
}

var rounds roundState

func stateFor(round uint32) *roundState {
	if rounds.round != round {
		rounds = roundState{round: round}
	}
	return &rounds
}

// getterSeen is set once get_auction_close has answered in this process, for any round. From then on a
// missing getter (exit code 11) is a failed read, never old rules. A restart forgets it and learns it
// again from the first answer.
var getterSeen atomic.Bool

// isMethodNotFound is true only for exit code 11.
func isMethodNotFound(err error) bool {
	var e ton.ContractExecError
	return errors.As(err, &e) && e.Code == exitMethodNotFound
}

// resolveRules decides old or new rules from the getter's outcome. Only exit code 11 before the getter
// has ever answered in this process selects old rules; any other error, and an exit code 11 after the
// getter has answered, is a failed read that the caller retries.
func resolveRules(err error) (newRules bool, failed error) {
	if err == nil {
		getterSeen.Store(true)
		return true, nil
	}
	if isMethodNotFound(err) && !getterSeen.Load() {
		return false, nil
	}
	return false, err
}

// RequestDeadline is C, the close counted from: min(E, D) while a request stands, D while the round
// has none (the treasury accepts a first bid until D).
func (a AuctionClose) RequestDeadline() uint32 {
	if a.Status == 0 || a.Close > a.ParticipateSince {
		return a.ParticipateSince
	}
	return a.Close
}

// PlanSend reports whether a request may still be sent at now, and the valid_until its wallet
// message carries: min(now + 180 s, C - 5 s). Nothing is sent at or after C - 60 s.
func PlanSend(now, deadline uint32) (validUntil uint32, ok bool) {
	if deadline < now || time.Duration(deadline-now)*time.Second <= lastSendMargin {
		return 0, false
	}
	validUntil = now + uint32(messageTTL/time.Second)
	if limit := deadline - uint32(expiryMargin/time.Second); limit < validUntil {
		validUntil = limit
	}
	return validUntil, true
}

// PollInterval is how soon to look again while answering: every 60 s until the close c minus 15 min,
// then every 15 s. After c it keeps looking every 15 s until d (participate_since), because a request
// sent at the last moment may have moved the close, and 0 once d has passed.
func PollInterval(now, c, d uint32) time.Duration {
	if now >= c {
		if now < d {
			return 15 * time.Second
		}
		return 0
	}
	zone := int64(c) - int64(extensionZone/time.Second)
	if int64(now) < zone {
		if left := time.Duration(zone-int64(now)) * time.Second; left < time.Minute {
			return left
		}
		return time.Minute
	}
	return 15 * time.Second
}

var errTooLate = errors.New("too close to the close to send")

// buildExpiring builds the wallet's external message so that its signed valid_until is at most
// deadline - 5 s. It sets a TTL from PlanSend, with room for the wallet's own reads, builds, reads the
// signed valid_until back, and builds again with more room if it is late. The cut-off is checked
// before each build.
func buildExpiring(deadline uint32, clock func() uint32, setTTL func(uint32) error,
	build func() (*tlb.ExternalMessage, error), validUntilOf func(*cell.Cell) (uint32, error)) (*tlb.ExternalMessage, error) {
	limit := deadline - uint32(expiryMargin/time.Second)
	allowance := uint32(3)
	for try := 0; try < 4; try++ {
		now := clock()
		validUntil, ok := PlanSend(now, deadline)
		if !ok {
			return nil, errTooLate
		}
		ttl := validUntil - now
		if ttl > allowance {
			ttl -= allowance
		}
		if err := setTTL(ttl); err != nil {
			return nil, err
		}
		ext, err := build()
		if err != nil {
			return nil, err
		}
		got, err := validUntilOf(ext.Body)
		if err != nil {
			return nil, err
		}
		if got <= limit {
			// Checked again after the build, right before it is sent: the build may have been slow.
			if _, ok := PlanSend(clock(), deadline); !ok {
				return nil, errTooLate
			}
			return ext, nil
		}
		allowance *= 2
	}
	return nil, errors.New("could not build a request that expires before the close")
}

// MessageValidUntil reads the valid_until of a signed wallet message body: v3r2 and v4r2 carry the
// signature first, then the subwallet, then valid_until; v5r1final carries op, wallet id, valid_until
// and the signature last.
func MessageValidUntil(spec any, body *cell.Cell) (uint32, error) {
	s, err := body.BeginParse()
	if err != nil {
		return 0, err
	}
	switch spec.(type) {
	case *wallet.SpecV5R1Final:
		if _, err = s.LoadUInt(64); err != nil {
			return 0, err
		}
	case *wallet.SpecV3, *wallet.SpecV4R2:
		if _, err = s.LoadSlice(512 + 32); err != nil {
			return 0, err
		}
	default:
		return 0, errors.New("this wallet version cannot be checked for its expiry")
	}
	v, err := s.LoadUInt(32)
	return uint32(v), err
}
