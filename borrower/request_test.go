package borrower

import (
	"encoding/hex"
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

// Parsed the way the treasury parses it: op, query_id, round_since, loan_amount, min_payment, then
// max_stake on a treasury that takes it, then nothing but the new_stake_msg ref, or end_parse throws.
func TestRequestBodyMatchesWhatTheTreasuryParses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxStake *big.Int
	}{
		{"before the stake cap", nil},
		{"no cap", big.NewInt(0)},
		{"capped", gram(t, "3000000")},
	} {
		t.Run(tc.name, func(t *testing.T) { checkRequestBody(t, tc.maxStake) })
	}
}

func checkRequestBody(t *testing.T, maxStake *big.Int) {
	newStakeMsg := cell.BeginCell().MustStoreUInt(7, 8).EndCell()
	loan, minPayment := gram(t, "1000000"), gram(t, "651")

	body := RequestBody(42, 1790000000, loan, minPayment, maxStake, newStakeMsg)
	s := body.MustBeginParse()
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
	if maxStake != nil {
		if c := s.MustLoadBigCoins(); c.Cmp(maxStake) != 0 {
			t.Errorf("max_stake = %v", c)
		}
	}
	if s.BitsLeft() != 0 {
		t.Errorf("%v bits left before the ref; the treasury's end_parse would refuse this", s.BitsLeft())
	}
	if ref := s.MustLoadRef().MustToCell(); string(ref.Hash()) != string(newStakeMsg.Hash()) {
		t.Error("new_stake_msg ref does not round-trip")
	}
	if s.RefsNum() != 0 {
		t.Errorf("%v trailing refs", s.RefsNum())
	}
}

func TestUnchangedComparesEveryBidField(t *testing.T) {
	r := Request{MinPayment: gram(t, "651"), RewardShare: 1799, LoanAmount: gram(t, "1000000"),
		MaxStake: gram(t, "3000000")}
	if !r.Unchanged(gram(t, "1000000"), gram(t, "651"), 1799, gram(t, "3000000")) {
		t.Error("an identical request should count as unchanged")
	}
	if r.Unchanged(gram(t, "1000000"), gram(t, "652"), 1799, gram(t, "3000000")) {
		t.Error("a different min_payment should be re-sent")
	}
	if r.Unchanged(gram(t, "999999"), gram(t, "651"), 1799, gram(t, "3000000")) {
		t.Error("a different loan should be re-sent")
	}
	if r.Unchanged(gram(t, "1000000"), gram(t, "651"), 2000, gram(t, "3000000")) {
		t.Error("a different share should be re-sent")
	}
	if r.Unchanged(gram(t, "1000000"), gram(t, "651"), 1799, big.NewInt(0)) {
		t.Error("a different cap should be re-sent")
	}
}

// A request cell exactly as the treasury's pack_request builds it, before and after the stake cap.
func TestLoadRequestReadsTheCapOnlyWhenStored(t *testing.T) {
	build := func(maxStake *big.Int) *cell.Cell {
		b := cell.BeginCell().MustStoreBigCoins(gram(t, "651")).MustStoreUInt(1799, 16).
			MustStoreBigCoins(gram(t, "1000000")).MustStoreBigCoins(gram(t, "5")).
			MustStoreBigCoins(gram(t, "753")).MustStoreUInt(32767, 16)
		if maxStake != nil {
			b.MustStoreBigCoins(maxStake)
		}
		return b.MustStoreRef(cell.BeginCell().EndCell()).EndCell()
	}
	old := LoadRequest(build(nil))
	if old.MaxStake.Sign() != 0 || old.BorrowerFee != 32767 || old.StakeAmount.Cmp(gram(t, "753")) != 0 {
		t.Errorf("a request stored before the cap misread: %+v", old)
	}
	capped := LoadRequest(build(gram(t, "3000000")))
	if capped.MaxStake.Cmp(gram(t, "3000000")) != 0 || capped.BorrowerFee != 32767 {
		t.Errorf("a capped request misread: %+v", capped)
	}
}

func TestTakesMaxStakeOnlyAfterTheStakeCapRelease(t *testing.T) {
	for h, takes := range map[string]bool{
		"6cd64455cf733d84a56da540b1ad757e966bdbe8146fe32d52c01efc038a8c6c": false, // reward share
		"f003de4b9ab34a61dd7d70a0a68a5faaf6ac0a8821ff2d720f9fecf8dd71475d": false, // accrual pricing
		"54d84afcf4201d5db915cf4cbc16a74f7d50df1ea71aa7e259e2b0fb9e134e59": true,  // stake cap
	} {
		b, _ := hex.DecodeString(h)
		if TakesMaxStake(b) != takes {
			t.Errorf("TakesMaxStake(%v...) = %v", h[:8], !takes)
		}
	}
}

func TestCapFitsWhatTheTreasuryAccepts(t *testing.T) {
	loan, collateral := gram(t, "300000"), gram(t, "500501") // own stake included
	if !CapFits(big.NewInt(0), loan, collateral) {
		t.Error("0 is no cap and always fits")
	}
	if !CapFits(gram(t, "800501"), loan, collateral) {
		t.Error("a cap equal to loan + collateral fits")
	}
	if CapFits(gram(t, "800500"), loan, collateral) {
		t.Error("a cap below loan + collateral is refused by the treasury")
	}
}

func TestSizeRequestReadsThePunishmentOnWhatTheTreasuryChecks(t *testing.T) {
	loan, minPayment := gram(t, "1000000"), gram(t, "651")
	flat := func(*big.Int) *big.Int { return gram(t, "101") }
	collateral, p := SizeRequest(loan, minPayment, big.NewInt(0), 32767, flat)
	if p.Cmp(gram(t, "101")) != 0 || collateral.Cmp(gram(t, "753")) != 0 {
		t.Errorf("flat fine: punishment %v, collateral %v; want 101 and 753", p, collateral)
	}

	// A fine that grows with the stake: 101 + 0.1% of it. The treasury checks it on loan + collateral.
	growing := func(s *big.Int) *big.Int {
		return new(big.Int).Add(gram(t, "101"), new(big.Int).Div(s, big.NewInt(1000)))
	}
	collateral, p = SizeRequest(loan, minPayment, big.NewInt(0), 32767, growing)
	checked := growing(new(big.Int).Add(loan, collateral))
	required := new(big.Int).Add(minPayment, checked)
	required.Add(required, minBurn)
	// One re-read is enough while the fine is a small fraction of the stake: within a nano of 0.1% of
	// the collateral's own growth.
	if shortBy := new(big.Int).Sub(required, collateral); shortBy.Cmp(gram(t, "0.01")) > 0 {
		t.Errorf("collateral %v is %v short of what the treasury checks (punishment %v)", collateral, shortBy, p)
	}
}

func TestCoversMinStake(t *testing.T) {
	minStake := gram(t, "300000")
	if !CoversMinStake(gram(t, "753"), gram(t, "300000"), minStake) {
		t.Error("a min_stake loan with collateral above 1 GRAM covers it")
	}
	if CoversMinStake(gram(t, "753"), gram(t, "299000"), minStake) {
		t.Error("a loan 1000 GRAM under min_stake with 753 of collateral does not")
	}
}

// A replacement carries the posted collateral forward, so it must not send it again.
func TestSendValueOnlyTopsUpAReplacement(t *testing.T) {
	fee, collateral := gram(t, "0.8"), gram(t, "753")
	if v := SendValue(collateral, fee, big.NewInt(0)); v.Cmp(gram(t, "753.8")) != 0 {
		t.Errorf("new request sends %v, want 753.8", v)
	}
	if v := SendValue(collateral, fee, gram(t, "753")); v.Cmp(fee) != 0 {
		t.Errorf("replacement with enough posted sends %v, want just the fee", v)
	}
	if v := SendValue(collateral, fee, gram(t, "700")); v.Cmp(gram(t, "53.8")) != 0 {
		t.Errorf("replacement 53 short sends %v, want 53.8", v)
	}
	if v := SendValue(collateral, fee, gram(t, "900")); v.Cmp(fee) != 0 {
		t.Errorf("replacement with more than enough posted sends %v, want just the fee", v)
	}
}
