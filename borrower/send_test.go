package borrower

import (
	"testing"
	"time"
)

// Re-sending on a view that predates the send would post a whole second collateral on top of a request
// that is in fact standing, so absence only counts once the view is provably later.
func TestAMissingRequestCountsOnlyInAViewAfterTheSend(t *testing.T) {
	at := time.Unix(1790000000, 0)
	s := sendRecord{round: 1, attempts: 1, at: at, lt: 1000}

	if !s.viewPrecedesSend(at.Add(time.Minute), 5000) {
		t.Error("a minute after the send is too soon to call the request refused")
	}
	if !s.viewPrecedesSend(at.Add(10*time.Minute), 1000) {
		t.Error("a view where the treasury has done nothing since the send has not seen it")
	}
	if s.viewPrecedesSend(at.Add(10*time.Minute), 1001) {
		t.Error("a later view in which the treasury has moved on has seen the send")
	}
}

func TestARoundGetsAtMostMaxSends(t *testing.T) {
	s := sendRecord{round: 1}
	for i := 0; i < maxSends; i++ {
		if s.exhausted() {
			t.Fatalf("exhausted after %v sends, want %v", i, maxSends)
		}
		s.attempts++
	}
	if !s.exhausted() {
		t.Fatalf("still sending after %v sends", maxSends)
	}
}
