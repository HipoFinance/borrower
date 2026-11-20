package borrower

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

func newEmptyCell() *cell.Cell { return cell.BeginCell().EndCell() }

func TestOnlyExit11MeansOldRules(t *testing.T) {
	getterSeen.Store(false)
	if nr, err := resolveRules(ton.ContractExecError{Code: 11}); nr || err != nil {
		t.Errorf("exit 11 first: newRules %v err %v, want old rules", nr, err)
	}
	for _, e := range []error{errors.New("timeout"), ton.ContractExecError{Code: 7}, ton.ContractExecError{Code: 0}} {
		if nr, err := resolveRules(e); nr || err == nil {
			t.Errorf("%v: newRules %v err %v, want a failed read", e, nr, err)
		}
	}
	wrapped := fmt.Errorf("lite: %w", ton.ContractExecError{Code: 11})
	if nr, err := resolveRules(wrapped); nr || err != nil {
		t.Errorf("wrapped exit 11 should still be old rules")
	}
}

func TestNewRulesAreNeverLeftInThisProcess(t *testing.T) {
	getterSeen.Store(false)
	defer getterSeen.Store(false)
	if nr, err := resolveRules(nil); !nr || err != nil {
		t.Fatal("getter answered: want new rules")
	}
	for _, e := range []error{ton.ContractExecError{Code: 11}, errors.New("timeout")} {
		if nr, err := resolveRules(e); nr || err == nil {
			t.Errorf("%v after new rules: newRules %v err %v, want a failed read, never old rules", e, nr, err)
		}
	}
	// Another round of the same process is still on new rules; a restart forgets and learns again.
	stateFor(5)
	if nr, err := resolveRules(ton.ContractExecError{Code: 11}); nr || err == nil {
		t.Error("a new round fell back to old rules")
	}
	getterSeen.Store(false)
	if nr, err := resolveRules(ton.ContractExecError{Code: 11}); nr || err != nil {
		t.Error("a fresh process should learn the getter is absent")
	}
}

func TestDeadlineCountsFromDForAFirstBid(t *testing.T) {
	cases := []struct {
		a    AuctionClose
		want uint32
	}{
		{AuctionClose{Status: 0, Close: 1000, BaseClose: 1000, ParticipateSince: 1500}, 1500},
		{AuctionClose{Status: 1, Close: 1000, BaseClose: 900, ParticipateSince: 1500}, 1000},
		{AuctionClose{Status: 1, Close: 2000, BaseClose: 900, ParticipateSince: 1500}, 1500},
	}
	for _, c := range cases {
		if got := c.a.RequestDeadline(); got != c.want {
			t.Errorf("%+v: deadline %v, want %v", c.a, got, c.want)
		}
	}
}

func TestPlanSend(t *testing.T) {
	const c = 100000
	if _, ok := PlanSend(c-59, c); ok {
		t.Error("a send 59 s before the close should be skipped")
	}
	if _, ok := PlanSend(c-60, c); ok {
		t.Error("a send at exactly C - 60 s should be skipped")
	}
	if _, ok := PlanSend(c+1, c); ok {
		t.Error("a send after the close should be skipped")
	}
	if vu, ok := PlanSend(c-61, c); !ok || vu != c-5 {
		t.Errorf("61 s before: ok %v valid_until %v, want %v", ok, vu, c-5)
	}
	if vu, ok := PlanSend(c-1000, c); !ok || vu != c-1000+180 {
		t.Errorf("far from the close: valid_until %v, want now + 180", vu)
	}
	if vu, ok := PlanSend(c-185, c); !ok || vu != c-5 {
		t.Errorf("valid_until %v, want the close - 5 s", vu)
	}
}

func TestPollInterval(t *testing.T) {
	const e, d = 100000, 100000 + 4*3600
	for _, c := range []struct {
		now  uint32
		want time.Duration
	}{{e - 3600, time.Minute}, {e - 900 - 20, 20 * time.Second}, {e - 900, 15 * time.Second},
		{e - 10, 15 * time.Second},
		// After the close in our view a last-second bid may have moved it: keep looking until D.
		{e, 15 * time.Second}, {e + 5, 15 * time.Second}, {d - 1, 15 * time.Second}, {d, 0}, {d + 5, 0}} {
		if got := PollInterval(c.now, e, d); got != c.want {
			t.Errorf("now %v: %v, want %v", c.now, got, c.want)
		}
	}
}

// The request message is bounceable, so a refused request returns its value.
func TestRequestMessageIsBounceable(t *testing.T) {
	treasury := address.MustParseAddr("EQCLyZHP4Xe8fpchQz76O-_RmUhaVc_9BAoGyJrwJrcbz2eZ")
	m := BuildRequestMessage(treasury, gram(t, "700"), newEmptyCell())
	if !m.InternalMessage.Bounce {
		t.Error("the request is not bounceable")
	}
	if m.InternalMessage.DstAddr.String() != treasury.String() {
		t.Error("wrong destination")
	}
}

// Every wallet version the borrower loads takes a per-message expiry, and it is the one written into
// the signed message as valid_until.
func TestWalletMessageExpiry(t *testing.T) {
	_, key, _ := ed25519GenerateKey()
	treasury := address.MustParseAddr("EQCLyZHP4Xe8fpchQz76O-_RmUhaVc_9BAoGyJrwJrcbz2eZ")
	for _, c := range []struct {
		name    string
		version wallet.VersionConfig
	}{{"v3r2", wallet.V3R2}, {"v4r2", wallet.V4R2}, {"v5r1final", wallet.ConfigV5R1Final{NetworkGlobalID: -239}}} {
		w, err := wallet.FromPrivateKey(nil, key, c.version)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if err := SetMessageTTL(w, 77); err != nil {
			t.Fatal(c.name, err)
		}
		type builder interface {
			BuildMessage(context.Context, bool, *ton.BlockIDExt, []*wallet.Message) (*cell.Cell, error)
			SetSeqnoFetcher(func(context.Context, uint32) (uint32, error))
		}
		spec, ok := w.GetSpec().(builder)
		if !ok {
			t.Fatalf("%s: spec cannot be built", c.name)
		}
		spec.SetSeqnoFetcher(func(context.Context, uint32) (uint32, error) { return 1, nil })
		msg := BuildRequestMessage(treasury, big.NewInt(1), newEmptyCell())
		built, err := spec.BuildMessage(context.Background(), true, nil, []*wallet.Message{msg})
		if err != nil {
			t.Fatal(c.name, err)
		}
		got, err := MessageValidUntil(w.GetSpec(), built)
		if err != nil {
			t.Fatal(c.name, err)
		}
		validUntil := uint64(got)
		want := time.Now().Unix() + 77
		if d := int64(validUntil) - want; d < -3 || d > 3 {
			t.Errorf("%s: valid_until %v, want about %v", c.name, validUntil, want)
		}
	}
}

func ed25519GenerateKey() (ed25519PublicKey, ed25519PrivateKey, error) {
	return ed25519.GenerateKey(nil)
}

type (
	ed25519PublicKey  = ed25519.PublicKey
	ed25519PrivateKey = ed25519.PrivateKey
)

// The wallet stamps now + TTL after its own liteserver reads. When the build is slow, the signed
// valid_until is read back and the message rebuilt, so what is sent never expires after C - 5 s.
func TestBuildExpiringKeepsTheSignedExpiryBeforeTheClose(t *testing.T) {
	const c = 100000
	for _, latency := range []uint32{0, 2, 9, 25} {
		now := uint32(c - 400)
		var ttl uint32
		builds := 0
		ext, err := buildExpiring(c, func() uint32 { return now },
			func(v uint32) error { ttl = v; return nil },
			func() (*tlb.ExternalMessage, error) {
				builds++
				// The wallet's reads take `latency` seconds before it stamps.
				stamped := now + latency + ttl
				now += latency
				return &tlb.ExternalMessage{Body: (&ext0{stamped}).msgBody()}, nil
			},
			func(b *cell.Cell) (uint32, error) { return uint32(b.MustBeginParse().MustLoadUInt(32)), nil })
		if err != nil {
			t.Fatalf("latency %v: %v", latency, err)
		}
		if got := uint32(ext.Body.MustBeginParse().MustLoadUInt(32)); got > c-5 {
			t.Errorf("latency %v: valid_until %v is after C - 5 s (%v) after %v builds", latency, got, c-5, builds)
		}
	}
	// A build that is slow enough to cross the cut-off is refused, not sent.
	late := uint32(c - 200)
	_, err0 := buildExpiring(c, func() uint32 { return late }, func(uint32) error { return nil },
		func() (*tlb.ExternalMessage, error) {
			late = c - 59
			return &tlb.ExternalMessage{Body: (&ext0{c - 100}).msgBody()}, nil
		},
		func(b *cell.Cell) (uint32, error) { return uint32(b.MustBeginParse().MustLoadUInt(32)), nil })
	if !errors.Is(err0, errTooLate) {
		t.Errorf("a build finished after the cut-off: err = %v, want errTooLate", err0)
	}
	// Close to the cut-off the message is refused, not sent.
	now := uint32(c - 60)
	_, err := buildExpiring(c, func() uint32 { return now }, func(uint32) error { return nil },
		func() (*tlb.ExternalMessage, error) { t.Fatal("built at the cut-off"); return nil, nil },
		func(*cell.Cell) (uint32, error) { return 0, nil })
	if !errors.Is(err, errTooLate) {
		t.Errorf("err = %v, want errTooLate", err)
	}
}

type ext0 struct{ validUntil uint32 }

func (e *ext0) msgBody() *cell.Cell {
	return cell.BeginCell().MustStoreUInt(uint64(e.validUntil), 32).EndCell()
}
