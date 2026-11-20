package borrower

import (
	"errors"
	"github.com/xssnick/tonutils-go/tlb"
	"math/big"
	"testing"
	"time"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

func requestCell(minPayment, loan *big.Int, share uint16) *cell.Cell {
	return cell.BeginCell().
		MustStoreBigCoins(minPayment).
		MustStoreUInt(uint64(share), 16).
		MustStoreBigCoins(loan).
		MustStoreBigCoins(big.NewInt(0)).
		MustStoreBigCoins(big.NewInt(0)).
		MustStoreUInt(0, 16).
		MustStoreBigCoins(big.NewInt(0)).
		MustStoreRef(newEmptyCell()).
		EndCell()
}

// A round with our request at 600 and one rival at 650, at the same loan.
func roundWith(t *testing.T, loan *big.Int, ours, rival *big.Int, rivalAddr int64) (*Participation, *Request) {
	t.Helper()
	d := cell.NewDict(256)
	key := func(a *big.Int) *cell.Cell { return cell.BeginCell().MustStoreBigUInt(a, 256).EndCell() }
	if err := d.Set(key(big.NewInt(500)), requestCell(ours, loan, 100)); err != nil {
		t.Fatal(err)
	}
	if err := d.Set(key(big.NewInt(rivalAddr)), requestCell(rival, loan, 100)); err != nil {
		t.Fatal(err)
	}
	r := LoadRequest(requestCell(ours, loan, 100))
	return &Participation{Requests: d}, &r
}

func TestConsiderAnswer(t *testing.T) {
	loan := gram(t, "1100000")
	ours, rival, max := MinPaymentFor(600, loan), MinPaymentFor(650, loan), MinPaymentFor(700, loan)
	cfg := func(f func(*Answering)) Answering {
		a := Answering{Respond: true, MaxMinPayment: max, MaxResponses: 10}
		if f != nil {
			f(&a)
		}
		return a
	}
	run := func(a Answering, st *roundState, rivalAddr int64) *big.Int {
		p, standing := roundWith(t, loan, ours, rival, rivalAddr)
		_, answer := considerAnswer(p, big.NewInt(500), standing, a, st, loan, ours, "r")
		return answer
	}

	if a := run(cfg(nil), &roundState{}, 7); a == nil || a.Cmp(MinPaymentFor(651, loan)) != 0 {
		t.Errorf("answer %v, want the smallest for 651", a)
	}
	if a := run(cfg(func(a *Answering) { a.Respond = false }), &roundState{}, 7); a != nil {
		t.Error("answered with respond off")
	}
	if a := run(cfg(func(a *Answering) { a.MaxMinPayment = ours }), &roundState{}, 7); a != nil {
		t.Error("answered with the ceiling at the starting bid")
	}
	if a := run(cfg(func(a *Answering) { a.Ignore = []*big.Int{big.NewInt(7)} }), &roundState{}, 7); a != nil {
		t.Error("answered a request from an ignored address")
	}
	if a := run(cfg(nil), &roundState{answers: 10}, 7); a != nil {
		t.Error("answered after max_responses answers")
	}
	if a := run(cfg(func(a *Answering) { a.MaxResponses = 0 }), &roundState{}, 7); a != nil {
		t.Error("answered with max_responses 0")
	}
	// A re-send of the answer already counted is not a further answer.
	if a := run(cfg(nil), &roundState{answers: 10, lastAnswer: MinPaymentFor(651, loan)}, 7); a == nil {
		t.Error("a re-send of the last answer was blocked by the count")
	}
	// A request below ours is not an outbid.
	p, standing := roundWith(t, loan, ours, MinPaymentFor(550, loan), 7)
	if _, a := considerAnswer(p, big.NewInt(500), standing, cfg(nil), &roundState{}, loan, ours, "r"); a != nil {
		t.Error("answered a request that does not outrank ours")
	}
}

func TestAnswerNeverLowersTheStandingBid(t *testing.T) {
	loan := gram(t, "1100000")
	high := MinPaymentFor(660, loan)
	p, standing := roundWith(t, loan, high, MinPaymentFor(500, loan), 7)
	got, answer := considerAnswer(p, big.NewInt(500), standing,
		Answering{Respond: true, MaxMinPayment: MinPaymentFor(700, loan), MaxResponses: 10}, &roundState{}, loan,
		MinPaymentFor(600, loan), "r")
	if answer != nil || got.Cmp(high) != 0 {
		t.Errorf("got %v answer %v, want the standing %v kept", got, answer, high)
	}
}

func TestLoadAnswering(t *testing.T) {
	min := gram(t, "650")
	a, err := LoadAnswering(Borrow{}, min)
	if err != nil || a.Respond || a.MaxMinPayment.Cmp(min) != 0 || a.MaxResponses != 10 {
		t.Errorf("defaults: %+v %v", a, err)
	}
	if _, err := LoadAnswering(Borrow{MaxMinPayment: "649"}, min); err == nil {
		t.Error("max_min_payment below min_payment was accepted")
	}
	neg := -1
	if _, err := LoadAnswering(Borrow{MaxResponses: &neg}, min); err == nil {
		t.Error("negative max_responses was accepted")
	}
	zero := 0
	if a, err := LoadAnswering(Borrow{Respond: true, MaxMinPayment: "700", MaxResponses: &zero,
		Ignore: []string{"EQCLyZHP4Xe8fpchQz76O-_RmUhaVc_9BAoGyJrwJrcbz2eZ"}}, min); err != nil || a.MaxResponses != 0 ||
		len(a.Ignore) != 1 || !a.Respond {
		t.Errorf("set: %+v %v", a, err)
	}
	if _, err := LoadAnswering(Borrow{Ignore: []string{"nope"}}, min); err == nil {
		t.Error("a bad ignore address was accepted")
	}
}

// A rival answers before each of our polls. Every answer of ours lands and shows in the next view, so it
// is a standing request that is outranked again, never a refused send: ten answers, no false
// "not taken any of the requests".
func TestAWarRunsToMaxResponsesWithoutExhaustingSends(t *testing.T) {
	loan := gram(t, "1100000")
	a := Answering{Respond: true, MaxMinPayment: MinPaymentFor(2000, loan), MaxResponses: 10}
	st := &roundState{}
	var rec sendRecord
	ours := MinPaymentFor(600, loan)
	rivalEff := uint64(650)
	answers := 0
	for i := 0; i < 30; i++ {
		p, standing := roundWith(t, loan, ours, MinPaymentFor(rivalEff, loan), 7)
		if rec.settle(1, standing, loan, 100, big.NewInt(0)) {
			rec = sendRecord{}
		}
		if rec.exhausted() {
			t.Fatalf("poll %d: sends exhausted after %d answers", i, answers)
		}
		_, answer := considerAnswer(p, big.NewInt(500), standing, a, st, loan, ours, "r")
		if answer == nil {
			break
		}
		st.recordAnswer(answer)
		answers++
		ours = answer
		rec.round, rec.minPayment, rec.collateral = 1, answer, big.NewInt(0)
		rec.attempts++
		rivalEff = Efficiency(answer, loan) + 1
	}
	if answers != 10 || st.answers != 10 {
		t.Errorf("answers = %d (counted %d), want 10", answers, st.answers)
	}
}

func TestSettleOnlyWhenTheViewShowsTheSend(t *testing.T) {
	loan := gram(t, "1100000")
	sentP := MinPaymentFor(650, loan)
	rec := sendRecord{round: 1, attempts: 2, minPayment: sentP, collateral: big.NewInt(0)}
	_, lagging := roundWith(t, loan, MinPaymentFor(600, loan), MinPaymentFor(700, loan), 7)
	if rec.settle(1, lagging, loan, 100, big.NewInt(0)) {
		t.Error("a view still showing the older bid was taken as the send standing")
	}
	_, shown := roundWith(t, loan, sentP, MinPaymentFor(700, loan), 7)
	if !rec.settle(1, shown, loan, 100, big.NewInt(0)) {
		t.Error("a view showing the send, outranked, was not taken as standing")
	}
	if rec.settle(2, shown, loan, 100, big.NewInt(0)) {
		t.Error("settled for another round")
	}
}

// A count before the send: a re-send of the same answer does not count twice, a new one does.
func TestRecordAnswerCountsBeforeTheWaitAndNotAReSend(t *testing.T) {
	st := &roundState{}
	st.recordAnswer(big.NewInt(5))
	st.recordAnswer(big.NewInt(5))
	st.recordAnswer(big.NewInt(6))
	if st.answers != 2 {
		t.Errorf("answers = %d, want 2", st.answers)
	}
}

func TestFailedRecountIsRetriedNotCounted(t *testing.T) {
	if _, ok := recountAnswers(func() ([]*big.Int, error) { return nil, errors.New("liteserver") }); ok {
		t.Error("a failed recount was reported as done")
	}
	n, ok := recountAnswers(func() ([]*big.Int, error) {
		return []*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3)}, nil
	})
	if !ok || n != 2 {
		t.Errorf("recount = %d %v, want 2 true", n, ok)
	}
}

// Paging back stops at the first page that reaches a transaction older than the round's bidding.
func TestScanStopsAtTheStartOfBidding(t *testing.T) {
	pagesRead := 0
	mk := func(now uint32, prev uint64) *tlb.Transaction { return &tlb.Transaction{Now: now, PrevTxLT: prev} }
	var limits []uint32
	list := func(lt uint64, hash []byte, limit uint32) ([]*tlb.Transaction, error) {
		pagesRead++
		limits = append(limits, limit)
		switch lt {
		case 100:
			return []*tlb.Transaction{mk(900, 99), mk(950, 90)}, nil
		case 99:
			return []*tlb.Transaction{mk(500, 50), mk(800, 80)}, nil // reaches before since
		}
		t.Errorf("read a page at lt %d, older than the start of bidding", lt)
		return nil, nil
	}
	var seen []uint32
	_, err := scanSent(list, 100, nil, 700, func(tx *tlb.Transaction) ([]*big.Int, error) {
		seen = append(seen, tx.Now)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pagesRead != 2 {
		t.Errorf("pages read = %d, want 2", pagesRead)
	}
	for _, n := range seen {
		if n < 700 {
			t.Errorf("looked into a transaction at %d, older than the start", n)
		}
	}
	if limits[0] >= 20 || limits[1] != 20 {
		t.Errorf("page limits %v: the first page should be small", limits)
	}
	if _, err := scanSent(func(uint64, []byte, uint32) ([]*tlb.Transaction, error) { return nil, errors.New("x") }, 5, nil, 0,
		nil); err == nil {
		t.Error("a listing failure was swallowed")
	}
}

// The collateral for what is sent covers that min_payment, not the configured one.
func TestCollateralIsSizedForTheMinPaymentSent(t *testing.T) {
	loan := gram(t, "1100000")
	punish := func(*big.Int) *big.Int { return gram(t, "101") }
	low, _ := SizeRequest(loan, gram(t, "650"), big.NewInt(0), 0, punish)
	high, _ := SizeRequest(loan, gram(t, "700"), big.NewInt(0), 0, punish)
	if high.Cmp(low) <= 0 {
		t.Error("a higher min_payment needs more collateral")
	}
}

// A re-send at the same price to top up collateral: a lagging view still shows the old collateral and is
// not the send standing; a view that shows the top-up is.
func TestSettleWaitsForTheCollateralTopUp(t *testing.T) {
	loan := gram(t, "1100000")
	price := MinPaymentFor(651, loan)
	rec := sendRecord{round: 1, attempts: 1, minPayment: price, collateral: gram(t, "900")}
	view := func(stake string) *Request {
		_, r := roundWith(t, loan, price, MinPaymentFor(500, loan), 7)
		r.StakeAmount = gram(t, stake)
		return r
	}
	if rec.settle(1, view("800"), loan, 100, big.NewInt(0)) {
		t.Error("a view with the old collateral settled a top-up that has not landed")
	}
	if !rec.settle(1, view("900"), loan, 100, big.NewInt(0)) {
		t.Error("a view with the top-up did not settle")
	}
	if !rec.settle(1, view("950"), loan, 100, big.NewInt(0)) {
		t.Error("a view with more than the top-up did not settle")
	}
}

// The configured bid was raised above the standing one: an "answer" at or below what is sent anyway is
// not an answer, and uses no slot.
func TestNoAnswerBelowTheConfiguredBid(t *testing.T) {
	loan := gram(t, "1100000")
	standingP, rivalP, configured := MinPaymentFor(600, loan), MinPaymentFor(605, loan), MinPaymentFor(620, loan)
	p, standing := roundWith(t, loan, standingP, rivalP, 7)
	a := Answering{Respond: true, MaxMinPayment: MinPaymentFor(700, loan), MaxResponses: 10}
	target, answer := considerAnswer(p, big.NewInt(500), standing, a, &roundState{}, loan, configured, "r")
	if answer != nil || target.Cmp(configured) != 0 {
		t.Errorf("target %v answer %v, want the configured bid and no answer", target, answer)
	}
}

// Hitting the page limit before the start of bidding is a failed recount, not a partial count.
func TestScanFailsWhenThePageLimitComesFirst(t *testing.T) {
	list := func(lt uint64, hash []byte, limit uint32) ([]*tlb.Transaction, error) {
		return []*tlb.Transaction{{Now: 1000, PrevTxLT: lt - 1}}, nil
	}
	_, err := scanSent(list, 1000, nil, 0, func(*tlb.Transaction) ([]*big.Int, error) { return nil, nil })
	if err == nil {
		t.Error("a scan cut off by the page limit returned no error")
	}
	// Reaching the start of the wallet's history is complete.
	list = func(lt uint64, hash []byte, limit uint32) ([]*tlb.Transaction, error) {
		return []*tlb.Transaction{{Now: 1000, PrevTxLT: 0}}, nil
	}
	if _, err := scanSent(list, 5, nil, 0, func(*tlb.Transaction) ([]*big.Int, error) { return nil, nil }); err != nil {
		t.Errorf("a complete scan failed: %v", err)
	}
}

// The send is recorded with lt 0 before the transaction is known, so the view guard holds on a failed wait.
func TestRecordedSendGuardsTheViewBeforeItsTransactionIsKnown(t *testing.T) {
	var rec sendRecord
	at := time.Unix(1790000000, 0)
	rec.record(1, big.NewInt(5), big.NewInt(7), at)
	rec.record(1, big.NewInt(6), big.NewInt(7), at)
	if rec.attempts != 2 || rec.lt != 0 || rec.minPayment.Int64() != 6 {
		t.Errorf("record = %+v", rec)
	}
	if !rec.viewPrecedesSend(at.Add(time.Minute), 1<<40) {
		t.Error("a view a minute after the send was not held back")
	}
}
