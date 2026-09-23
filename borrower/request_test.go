package borrower

import (
	"math/big"
	"testing"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

// request_loan in the treasury refuses unless value - request fee covers
// min_payment + max_punishment, plus fee::min_burn while a borrower fee is in force.
func TestRequestValueCoversWhatTheTreasuryChecks(t *testing.T) {
	fee := gram(t, "0.724347680")
	minPayment := gram(t, "651")
	for _, c := range []struct {
		name        string
		punishment  string
		borrowerFee uint16
	}{
		{"flat punishment, fee on", "101", 32767},
		{"flat punishment, fee off", "101", 0},
		{"punishment under the 1 GRAM floor", "0.5", 32767},
	} {
		punishment := gram(t, c.punishment)
		value := RequestValue(punishment, fee, minPayment, big.NewInt(0), c.borrowerFee)

		collateral := new(big.Int).Sub(value, fee)
		required := new(big.Int).Add(minPayment, punishment)
		if c.borrowerFee != 0 {
			required.Add(required, minBurn)
		}
		if collateral.Cmp(required) < 0 {
			t.Errorf("%s: collateral %v is below the treasury's requirement %v", c.name, collateral, required)
		}
	}
}

func TestRequestValueAddsOwnStakeAndLeavesItsInputsAlone(t *testing.T) {
	punishment := gram(t, "101")
	before := new(big.Int).Set(punishment)
	withStake := RequestValue(punishment, gram(t, "1"), gram(t, "500"), gram(t, "1000"), 0)
	without := RequestValue(punishment, gram(t, "1"), gram(t, "500"), big.NewInt(0), 0)

	if diff := new(big.Int).Sub(withStake, without); diff.Cmp(gram(t, "1000")) != 0 {
		t.Errorf("own stake should add exactly itself, added %v", diff)
	}
	if punishment.Cmp(before) != 0 {
		t.Errorf("RequestValue changed its maxPunishment argument to %v", punishment)
	}
}

// Parsed the way the treasury parses it: op, query_id, round_since, loan_amount, min_payment, then --
// on a treasury that sets the share -- nothing but the new_stake_msg ref, or end_parse throws.
func TestRequestBodyMatchesWhatTheTreasuryParses(t *testing.T) {
	newStakeMsg := cell.BeginCell().MustStoreUInt(7, 8).EndCell()
	loan, minPayment := gram(t, "1000000"), gram(t, "651")

	for _, protocolSetsShare := range []bool{true, false} {
		body := RequestBody(42, 1790000000, loan, minPayment, 1799, protocolSetsShare, newStakeMsg)
		s := body.BeginParse()
		if op := s.MustLoadUInt(32); op != OpRequestLoan {
			t.Fatalf("op = %x", op)
		}
		if q := s.MustLoadUInt(64); q != 42 {
			t.Errorf("query_id = %v", q)
		}
		if r := s.MustLoadUInt(32); r != 1790000000 {
			t.Errorf("round_since = %v", r)
		}
		if l := s.MustLoadBigCoins(); l.Cmp(loan) != 0 {
			t.Errorf("loan_amount = %v", l)
		}
		if m := s.MustLoadBigCoins(); m.Cmp(minPayment) != 0 {
			t.Errorf("min_payment = %v", m)
		}
		if protocolSetsShare {
			if s.BitsLeft() != 0 {
				t.Errorf("%v bits left before the ref; the treasury's end_parse would refuse this", s.BitsLeft())
			}
		} else if share := s.MustLoadUInt(16); share != 1799 {
			t.Errorf("borrower_reward_share = %v", share)
		}
		if ref := s.MustLoadRef().MustToCell(); string(ref.Hash()) != string(newStakeMsg.Hash()) {
			t.Error("new_stake_msg ref does not round-trip")
		}
		if s.BitsLeft() != 0 || s.RefsNum() != 0 {
			t.Errorf("trailing data: %v bits, %v refs", s.BitsLeft(), s.RefsNum())
		}
	}
}

func TestUnchangedComparesEveryBidField(t *testing.T) {
	r := Request{MinPayment: gram(t, "651"), RewardShare: 1799, LoanAmount: gram(t, "1000000")}
	if !r.Unchanged(gram(t, "1000000"), gram(t, "651"), 1799) {
		t.Error("an identical request should count as unchanged")
	}
	if r.Unchanged(gram(t, "1000000"), gram(t, "652"), 1799) {
		t.Error("a different min_payment should be re-sent")
	}
	if r.Unchanged(gram(t, "999999"), gram(t, "651"), 1799) {
		t.Error("a different loan should be re-sent")
	}
	if r.Unchanged(gram(t, "1000000"), gram(t, "651"), 2000) {
		t.Error("a different share should be re-sent")
	}
}
