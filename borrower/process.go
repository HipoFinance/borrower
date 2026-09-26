package borrower

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

func Process() (wait time.Duration) {
	defer func() {
		if err := recover(); err != nil {
			wait = 0
			log.Printf("❌ %s", err)
			resetApi()
		}
	}()

	config := loadConfig()

	api, ctx := loadApi(config)

	treasuryAddress := address.MustParseAddr(config.Treasury)

	mainchainInfo := loadMainchainInfo(api, ctx)

	validatorsElectedFor, _, _, currentVsetHash, nextRoundSince, _ := loadBlockchainConfig(api, ctx, mainchainInfo)

	participations, _, _, _ := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)

	participateSince := getParticipateSince(api, ctx, mainchainInfo, treasuryAddress)

	participationsList := []cell.DictKV{}
	if participations != nil {
		all, err := participations.LoadAll()
		if err != nil {
			panic(fmt.Sprintf("Error in loading participations: %v", err))
		}
		participationsList = all
	}

	for _, kv := range participationsList {
		roundSince := uint32(kv.Key.MustLoadUInt(32))
		participation := LoadParticipation(kv.Value.MustToCell())
		formattedRoundSince := time.Unix(int64(roundSince), 0).Format(TimeFormat)
		log.Printf("ℹ️  Round: %v, state: %v", formattedRoundSince, participation.State)
		roundParticipateTime := participateSince
		if roundSince < participateSince {
			roundParticipateTime = roundSince
		}
		now := uint32(time.Now().Unix())
		vsetChanged := participation.CurrentVsetHash.Cmp(currentVsetHash) != 0
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		switch participation.State {
		case ParticipationOpen:
			if now < roundParticipateTime {
				next := time.Until(time.Unix(int64(roundParticipateTime), 0))
				if wait == 0 || wait > next {
					wait = next
				}
			} else {
				err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
					DstAddr: treasuryAddress,
					Body: cell.BeginCell().
						MustStoreUInt(ParticipateInElection, 32).
						MustStoreUInt(uint64(now), 64).
						MustStoreUInt(uint64(roundSince), 32).
						EndCell(),
				})
				if err != nil {
					log.Printf("⚠️  Failed to send participate_in_election for round %v", formattedRoundSince)
				} else {
					log.Printf("☑️  Sent participate_in_election for round %v", formattedRoundSince)
				}
				next := 30 * time.Second
				if wait == 0 || wait > next {
					wait = next
				}
			}

		case ParticipationDistributing:
			next := 30 * time.Second
			if wait == 0 || wait > next {
				wait = next
			}

		case ParticipationStaked:
			if !vsetChanged {
				next := time.Until(time.Unix(int64(nextRoundSince), 0))
				if wait == 0 || wait > next {
					wait = next
				}
			} else {
				err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
					DstAddr: treasuryAddress,
					Body: cell.BeginCell().
						MustStoreUInt(VsetChanged, 32).
						MustStoreUInt(uint64(now), 64).
						MustStoreUInt(uint64(roundSince), 32).
						EndCell(),
				})
				if err != nil {
					log.Printf("⚠️  Failed to send validating vset_changed for round %v", formattedRoundSince)
				} else {
					log.Printf("☑️  Sent validating vset_changed for round %v", formattedRoundSince)
				}
				next := 30 * time.Second
				if wait == 0 || wait > next {
					wait = next
				}
			}

		case ParticipationValidating:
			if !vsetChanged {
				next := time.Until(time.Unix(int64(roundSince+validatorsElectedFor), 0))
				if wait == 0 || wait > next {
					wait = next
				}
			} else {
				err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
					DstAddr: treasuryAddress,
					Body: cell.BeginCell().
						MustStoreUInt(VsetChanged, 32).
						MustStoreUInt(uint64(now), 64).
						MustStoreUInt(uint64(roundSince), 32).
						EndCell(),
				})
				if err != nil {
					log.Printf("⚠️  Failed to send held vset_changed for round %v", formattedRoundSince)
				} else {
					log.Printf("☑️  Sent held vset_changed for round %v", formattedRoundSince)
				}
				next := 30 * time.Second
				if wait == 0 || wait > next {
					wait = next
				}
			}

		case ParticipationHeld:
			if now < participation.StakeHeldUntil {
				next := time.Until(time.Unix(int64(participation.StakeHeldUntil), 0))
				if wait == 0 || wait > next {
					wait = next
				}

			} else {
				err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
					DstAddr: treasuryAddress,
					Body: cell.BeginCell().
						MustStoreUInt(FinishParticipation, 32).
						MustStoreUInt(uint64(now), 64).
						MustStoreUInt(uint64(roundSince), 32).
						EndCell(),
				})
				if err != nil {
					log.Printf("⚠️  Failed to send finish_participation for round %v", formattedRoundSince)
				} else {
					log.Printf("☑️  Sent finish_participation for round %v", formattedRoundSince)
				}
				next := 30 * time.Second
				if wait == 0 || wait > next {
					wait = next
				}
			}
		}
	}

	t := participateSince + 60
	if uint32(time.Now().Unix()) > t {
		t = nextRoundSince
	}
	next := time.Until(time.Unix(int64(t), 0))
	if wait == 0 || wait > next {
		wait = next
	}

	return
}

// DryRun makes RequestLoan stop before it touches the validator engine or the wallet: it reads the
// chain, works out the request it would send, logs it, and returns. See the -dry-run flag in main.go.
var DryRun bool

// lastRequestError is the error the most recent RequestLoan recovered from, for a dry run to report.
var lastRequestError error

// LastRequestError returns the error the most recent RequestLoan pass failed with, or nil.
func LastRequestError() error { return lastRequestError }

func RequestLoan() (wait time.Duration) {
	lastRequestError = nil
	defer func() {
		if err := recover(); err != nil {
			wait = 0
			lastRequestError = fmt.Errorf("%v", err)
			log.Printf("   ❌ %s", err)
			resetApi()
		}
	}()

	config := loadConfig()

	api, ctx := loadApi(config)

	engine := NewValidatorEngine(config.ValidatorEngine)

	treasuryAddress := address.MustParseAddr(config.Treasury)

	checkLiteserverIsSync(engine)

	mainchainInfo := loadMainchainInfo(api, ctx)

	validatorsElectedFor, minStake, maxStakeFactor, _, nextRoundSince, stakeHeldFor :=
		loadBlockchainConfig(api, ctx, mainchainInfo)

	participations, stopped, borrowerFee, protocolShare := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)
	if protocolShare == nil {
		panic("Error, the treasury has no protocol-set reward share (index 26 of get_treasury_state); " +
			"this borrower needs a treasury from 2026-09-21 or later")
	}
	rewardShare := *protocolShare

	formattedNextRoundSince := time.Unix(int64(nextRoundSince), 0).Format(TimeFormat)

	wait = time.Until(time.Unix(int64(nextRoundSince+stakeHeldFor+60), 0))

	if !config.Borrow.Active {
		return notSending(0, "↩️", "Borrow config is inactive")
	}

	adnlAddressBigInt := loadAdnlAddress(config.ValidatorEngine.AdnlAddress)

	w := loadWallet(config.Wallet, api, loadGlobalID(api, ctx, mainchainInfo))

	validatorAddress := w.Address()
	validatorAddress.SetTestnetOnly(treasuryAddress.IsTestnetOnly())
	validatorKey := cell.BeginCell().MustStoreBigUInt(new(big.Int).SetBytes(validatorAddress.Data()), 256).EndCell()

	loanAddress := loadLoanAddress(validatorAddress, treasuryAddress, nextRoundSince, api, ctx, mainchainInfo)

	stake, loan, minPayment, maxStake, maxFactor := loadBorrowConfig(config.Borrow, minStake, maxStakeFactor)

	requestLoanFee := getRequestLoanFee(api, ctx, mainchainInfo, treasuryAddress)

	if stopped {
		return notSending(0, "🔲", "Treasury is stopped")
	}

	participation := loadParticipation(participations, nextRoundSince)
	if participation.State != ParticipationOpen {
		// Includes a round that closed after our last send: decide_loan_requests has moved every
		// request out of `requests`, so its absence says nothing about whether ours landed.
		return notSending(wait, "⏩", fmt.Sprintf("Loan requests are not accepted at the moment for round %v",
			formattedNextRoundSince))
	}

	collateral, _ := SizeRequest(loan, minPayment, stake, borrowerFee, func(s *big.Int) *big.Int {
		return getMaxPunishment(api, ctx, mainchainInfo, treasuryAddress, s)
	})
	if !CoversMinStake(collateral, loan, minStake) {
		return notSending(0, "⚠️ ", fmt.Sprintf("A loan of %v GRAM with %v GRAM of collateral is under the "+
			"network's min_stake of %v GRAM plus 1; the treasury would refuse it. Raise borrow.loan or borrow.stake",
			tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(collateral).String(), tlb.FromNanoTON(minStake).String()))
	}

	standing := loadStandingRequest(participation, validatorKey)
	// A standing request is kept only while it is the bid this config makes AND its collateral still
	// covers what the treasury checks: a higher punishment or more own stake needs a top-up.
	if standing != nil && standing.Unchanged(loan, minPayment, rewardShare, maxStake) &&
		standing.StakeAmount.Cmp(collateral) >= 0 {
		if sent.round == nextRoundSince {
			log.Printf("   ✔️  The request sent for round %v is standing", formattedNextRoundSince)
			sent = sendRecord{}
		}
		log.Printf("   ⏩ Already participated in round %v", formattedNextRoundSince)
		return
	}
	if sent.round == nextRoundSince {
		// Our last send has not produced the bid it carried. Before calling it refused, make sure this
		// view of the chain is later than the send: a lagging liteserver shows the round as it was
		// before our message arrived, and re-sending on that would post a whole second collateral.
		if sent.viewPrecedesSend(time.Now(), treasuryLastLT(api, ctx, mainchainInfo, treasuryAddress)) {
			log.Printf("   🕐 Round %v does not show the request sent at %v yet; looking again in %v",
				formattedNextRoundSince, sent.at.Format(TimeFormat), standingCheck)
			return standingCheck
		}
		if sent.exhausted() {
			return notSending(wait, "❌", fmt.Sprintf("The treasury has not taken any of the %v requests sent "+
				"for round %v; not sending again this round. Check the wallet's and the treasury's "+
				"transactions", sent.attempts, formattedNextRoundSince))
		}
		log.Printf("   ❌ The request sent for round %v is not standing: the treasury refused it or it never "+
			"left the wallet. Sending again (%v of %v)", formattedNextRoundSince, sent.attempts+1, maxSends)
	} else if standing != nil {
		log.Printf("   ✏️  Updating last request to min_payment: %v, reward_share: %v, loan: %v, max_stake: %v",
			minPayment, rewardShare, loan, maxStake)
	}

	participateSince := getParticipateSince(api, ctx, mainchainInfo, treasuryAddress)
	if left := time.Until(time.Unix(int64(participateSince), 0)); left < sendMargin {
		// request_loan refuses from participate_since on, even while the round is still open, and the
		// refusal would look like a successful send from here. A send takes up to a couple of minutes
		// between the engine, the wallet and delivery, and the host clock may be behind, so bidding stops
		// short of the close.
		return notSending(wait, "⏩", fmt.Sprintf("Bidding for round %v closes at %v, too close to send",
			formattedNextRoundSince, time.Unix(int64(participateSince), 0).Format(TimeFormat)))
	}
	posted := big.NewInt(0)
	if standing != nil {
		posted = standing.StakeAmount
	}
	value := SendValue(collateral, requestLoanFee, posted)

	// What the treasury will hold as stake_amount after this send: a replacement only tops up.
	staked := collateral
	if posted.Cmp(staked) > 0 {
		staked = posted
	}
	held := new(big.Int).Add(staked, requestLoanFee)
	if !CapFits(maxStake, loan, held) {
		return notSending(0, "⚠️ ", fmt.Sprintf("borrow.max_stake of %v GRAM is below the loan, the collateral "+
			"and the request fee, %v GRAM; the treasury could refuse it. Raise it, or set it to 0 for no cap",
			tlb.FromNanoTON(maxStake).String(), tlb.FromNanoTON(new(big.Int).Add(loan, held)).String()))
	}

	// The value, and the wallet's own fees for sending it on top. The wallet sends with
	// pay-gas-separately and ignore-errors, so a balance that covers the value but not the fees drops
	// the message while the wallet's transaction still confirms.
	need := new(big.Int).Add(value, walletHeadroom)
	balance := loadBalance(w, mainchainInfo)
	if balance.Cmp(need) < 0 {
		return notSending(0, "⚠️ ", fmt.Sprintf("Low balance, need at least %v GRAM, but your wallet balance is %v GRAM",
			tlb.FromNanoTON(need).String(), tlb.FromNanoTON(balance).String()))
	}

	if DryRun {
		log.Printf("   🧪 Dry run: would request a loan of %v GRAM at min_payment %v, sending %v GRAM, "+
			"for validation round %v", tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(minPayment).String(),
			tlb.FromNanoTON(value).String(), formattedNextRoundSince)
		log.Printf("   🧪 Bid: %v", BidRate(minPayment, loan))
		log.Printf("   🧪 %v", describeCap(maxStake))
		log.Printf("   🧪 Stopping before the validator engine is configured or the wallet sends anything")
		return
	}

	log.Printf("   🛠  Configuring validator engine for round %v", formattedNextRoundSince)

	keyHash, publicKey :=
		createValidationKey(engine, nextRoundSince, validatorsElectedFor, config.ValidatorEngine.AdnlAddress)

	log.Printf("   💎 Requesting a loan of %v GRAM, sending %v GRAM, for validation round %v",
		tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(value), formattedNextRoundSince)
	// From the accrual-pricing treasury release on, min_payment is scaled to everything the treasury
	// lends the loan, leftover included, so the rate is what this bid promises -- see "Pricing a bid"
	// in the README.
	log.Printf("   🏷  Bid: %v", BidRate(minPayment, loan))
	log.Printf("   🧢 %v", describeCap(maxStake))

	confirmation := buildStakeConfirmation(nextRoundSince, maxFactor, loanAddress, adnlAddressBigInt)

	signature := engine.Sign(keyHash, confirmation)

	newStakeMsg := buildNewStakeMsg(publicKey, nextRoundSince, maxFactor, adnlAddressBigInt, signature)

	payload := RequestBody(uint64(time.Now().Unix()), nextRoundSince, loan, minPayment, maxStake, newStakeMsg)

	message := wallet.SimpleMessage(treasuryAddress, tlb.FromNanoTON(value), payload)

	tx := sendRequestLoan(w, message)

	// Sent is not standing: the treasury may still refuse it. Look again shortly, while there is time
	// to send again before bidding closes.
	if sent.round != nextRoundSince {
		sent = sendRecord{round: nextRoundSince}
	}
	sent.attempts++
	sent.at = time.Now()
	sent.lt = tx.LT
	log.Printf("   ✅ Sent a loan request for round %v; checking that it stands in %v", formattedNextRoundSince,
		standingCheck)

	return standingCheck
}

// standingCheck is how soon after a send the round's requests are read to confirm the request landed.
const standingCheck = 2 * time.Minute

// sendMargin is how long before participate_since this borrower stops sending.
const sendMargin = 3 * time.Minute

// maxSends is how many requests one round may be sent before the borrower stops and says so. A
// refusal that repeats -- a round over max_validators that evicts ours at once, say, which keeps the
// whole fee -- must not be paid for every two minutes until bidding closes.
const maxSends = 3

// sendRecord is the last send for a round, until a later pass finds it standing. In memory only: a
// restart forgets it, and the next pass treats the round afresh.
type sendRecord struct {
	round    uint32
	attempts int
	at       time.Time
	// lt is the wallet transaction that sent it. The treasury processes the message after it, so a
	// view in which the treasury's last transaction is no later than this has not seen it yet.
	lt uint64
}

var sent sendRecord

// viewPrecedesSend reports whether a view of the chain may predate the send, so that the request's
// absence from it proves nothing: too soon after the send, or the treasury's last transaction in the
// view is no later than the wallet transaction that sent it.
func (s sendRecord) viewPrecedesSend(now time.Time, treasuryLT uint64) bool {
	return now.Sub(s.at) < standingCheck || treasuryLT <= s.lt
}

// exhausted reports whether the round has had all the sends it may have.
func (s sendRecord) exhausted() bool {
	return s.attempts >= maxSends
}

// treasuryLastLT is the logical time of the treasury's last transaction in this view of the chain.
func treasuryLastLT(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address) uint64 {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	account, err := api.GetAccount(ctx, mainchainInfo, treasuryAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury account: %v", err))
	}
	return account.LastTxLT
}

// describeCap says what the request carries for max_stake.
func describeCap(sendCap *big.Int) string {
	switch {
	case sendCap.Sign() == 0:
		return "max_stake: 0 (no cap: the treasury may lend this loan any share of its leftover)"
	default:
		return fmt.Sprintf("max_stake: %v GRAM (loan + accrue + collateral, own stake included)",
			tlb.FromNanoTON(sendCap).String())
	}
}

// walletHeadroom is what the wallet must hold beyond the value it attaches, for its own fees.
var walletHeadroom = big.NewInt(1000000000) // 1 GRAM

// ErrWouldNotSend is what a dry run reports when a real pass would have sent nothing.
var ErrWouldNotSend = errors.New("a real run would not send a request")

// notSending logs why this pass sends nothing, and in a dry run records it, so that -dry-run exits
// non-zero for a configuration that would never bid.
func notSending(wait time.Duration, icon, why string) time.Duration {
	log.Printf("   %v %v", icon, why)
	if DryRun {
		lastRequestError = fmt.Errorf("%w: %v", ErrWouldNotSend, why)
	}
	return wait
}

// loadStandingRequest is this borrower's request in the round, or nil when there is none. A
// dictionary that fails to parse is an error, not an absent request: read as absent, it would send a
// full second collateral.
func loadStandingRequest(participation *Participation, validatorKey *cell.Cell) *Request {
	if participation.Requests == nil {
		return nil
	}
	s, err := participation.Requests.LoadValue(validatorKey)
	if errors.Is(err, cell.ErrNoSuchKeyInDict) {
		return nil
	}
	if err != nil {
		panic(fmt.Sprintf("Error in reading the round's requests: %v", err))
	}
	r := LoadRequest(s.MustToCell())
	return &r
}

func loadConfig() *Config {
	config, err := ReadConfig()
	if err != nil {
		panic(fmt.Sprintf("Error in reading borrower.yaml: %v", err))
	}
	return config
}

// One connection pool for the life of the process. Every pool connects to each liteserver in the
// global config and starts its own ping and listener goroutines, which keep it alive until Stop; this
// used to build a fresh pool on every pass, about twice a minute, and never stop any of them. The pool
// is dropped only after a failed pass, so that the next pass connects afresh -- which is all that
// building a new one each time was buying.
var (
	apiMu     sync.Mutex
	apiPool   *liteclient.ConnectionPool
	apiClient ton.APIClientWrapped
	apiSource string
)

func loadApi(config *Config) (ton.APIClientWrapped, context.Context) {
	apiMu.Lock()
	defer apiMu.Unlock()
	if apiPool == nil || apiSource != config.GlobalConfig {
		if apiPool != nil {
			apiPool.Stop()
		}
		apiPool, apiClient = nil, nil
		pool := liteclient.NewConnectionPool()
		if err := pool.AddConnectionsFromConfigFile(config.GlobalConfig); err != nil {
			pool.Stop()
			panic(fmt.Sprintf("Error in loading global config: %v", err))
		}
		apiPool, apiClient, apiSource = pool, ton.NewAPIClient(pool).WithRetry(10), config.GlobalConfig
	}
	return apiClient, apiPool.StickyContext(context.Background())
}

// resetApi stops the shared pool, so the next pass connects afresh.
func resetApi() {
	apiMu.Lock()
	defer apiMu.Unlock()
	if apiPool != nil {
		apiPool.Stop()
	}
	apiPool, apiClient = nil, nil
}

func checkLiteserverIsSync(engine *Engine) {
	isSync := engine.IsSync()
	if !isSync {
		panic("Error, liteserver is out of sync")
	}
}

func loadMainchainInfo(api ton.APIClientWrapped, ctx context.Context) *ton.BlockIDExt {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	mainchainInfo, err := api.CurrentMasterchainInfo(ctx)
	if err != nil {
		panic(fmt.Sprintf("Error in getting current masterchain info: %v", err))
	}
	return mainchainInfo
}

func loadBlockchainConfig(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt) (
	uint32, *big.Int, uint32, *big.Int, uint32, uint32) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	blockchainConfig, err :=
		api.GetBlockchainConfig(ctx, mainchainInfo, ConfigElection, ConfigCurrentValidators, ConfigStake)
	if err != nil {
		panic(fmt.Sprintf("Error in getting blockchain config: %v", err))
	}

	validatorsElectedFor, _, _, stakeHeldFor := GetElectionConfig(blockchainConfig.Get(ConfigElection))
	stakeConfig := blockchainConfig.Get(ConfigStake)
	minStake := GetMinStake(stakeConfig)
	maxStakeFactor := GetMaxStakeFactor(stakeConfig)
	currentValidators := blockchainConfig.Get(ConfigCurrentValidators)
	currentVsetHash := new(big.Int).SetBytes(currentValidators.Hash())
	_, nextRoundSince := GetVsetTimes(currentValidators)

	return validatorsElectedFor, minStake, maxStakeFactor, currentVsetHash, nextRoundSince, stakeHeldFor
}

func loadTreasuryState(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address) (*cell.Dictionary, bool, uint16, *uint16) {

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	treasuryAccount, err := api.GetAccount(ctx, mainchainInfo, treasuryAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury account: %v", err))
	}

	if !treasuryAccount.IsActive {
		panic("Error, treasury account is not active")
	}

	treasuryState, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_treasury_state")
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury state: %v", err))
	}

	// get_treasury_state is append-only since the 2026-09-07 release (it no longer mirrors storage
	// order). The 28 values are:
	//
	//    0 total_coins             7 participations   14 window_duration     21 collection_codes
	//    1 total_tokens            8 rounds_imbalance 15 last_settled_round  22 bill_codes
	//    2 total_staking           9 stopped?         16 halter              23 old_parents
	//    3 total_unstaking        10 instant_mint?    17 governor            24 mid_rate
	//    4 total_borrowers_stake  11 loan_codes       18 proposed_governor   25 mid_round
	//    5 deficit                12 previous_rate    19 governance_fee      26 reward_share
	//    6 parent                 13 current_rate     20 borrower_fee        27 total_request_fees
	//
	// This reads 7, 9, 20 and 26. The tuple has been append-only since the 2026-09-07 release, so
	// these indices hold; a shift would show up as tonutils-go's "incorrect result type" on the first
	// read below, because the value landing at index 7 stops being a cell. Check the tuple against
	// contract/contracts/treasury.fc's get_treasury_state before assuming the node is at fault.
	var participations *cell.Dictionary
	if !treasuryState.MustIsNil(7) {
		participations, err = treasuryState.MustCell(7).MustBeginParse().ToDict(32)
	}
	if err != nil {
		panic(fmt.Sprintf("Error in loading participations dictionary: %v", err))
	}

	stopped := treasuryState.MustInt(9).Cmp(big.NewInt(0)) != 0

	// Index 20, right after governance_fee. Out of 65535 of a borrower's contractual share of the
	// round reward, charged on top of what the pool takes and paid out of the borrower's own funds.
	// Zero disables it entirely, floor included.
	borrowerFee := uint16(treasuryState.MustInt(20).Uint64())

	// Index 26: the reward share every loan carries, set by the protocol since the 2026-09-21
	// release. Nil on a treasury without it, which RequestLoan refuses to bid on: such a treasury still
	// expects the share in the request, and this borrower no longer sends one. Process does not need
	// it, and keeps driving rounds either way.
	var rewardShare *uint16
	if len(treasuryState.AsTuple()) > rewardShareIndex {
		share := uint16(treasuryState.MustInt(rewardShareIndex).Uint64())
		rewardShare = &share
	}

	return participations, stopped, borrowerFee, rewardShare
}

const rewardShareIndex = 26

func getParticipateSince(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address) uint32 {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	treasuryAccount, err := api.GetAccount(ctx, mainchainInfo, treasuryAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury account: %v", err))
	}

	if !treasuryAccount.IsActive {
		panic("Error, treasury account is not active")
	}

	times, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_times")
	if err != nil {
		panic(fmt.Sprintf("Error in getting times: %v", err))
	}

	return uint32(times.MustInt(1).Int64())
}

func getMaxPunishment(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address, loan *big.Int) *big.Int {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	treasuryAccount, err := api.GetAccount(ctx, mainchainInfo, treasuryAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury account: %v", err))
	}

	if !treasuryAccount.IsActive {
		panic("Error, treasury account is not active")
	}

	maxPunishment, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_max_punishment", loan)
	if err != nil {
		panic(fmt.Sprintf("Error in getting max punishment: %v", err))
	}

	return maxPunishment.MustInt(0)
}

func getRequestLoanFee(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address) *big.Int {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	treasuryAccount, err := api.GetAccount(ctx, mainchainInfo, treasuryAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury account: %v", err))
	}

	if !treasuryAccount.IsActive {
		panic("Error, treasury account is not active")
	}

	// Read, not assumed. request_loan takes the fee out of the incoming value and keeps the rest as
	// collateral, so a fee larger than what this borrower attached makes the treasury's collateral
	// check fail and the request bounce -- while the send itself still looks successful here. The
	// value used to be hard-coded at 1 GRAM, which was above the live fee (0.724 GRAM in September
	// 2026) only by luck; gas prices are the network's, not ours.
	treasuryFees, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_treasury_fees", 0)
	if err != nil {
		panic(fmt.Sprintf("Error in getting treasury fees: %v", err))
	}

	// Ten percent of slack, so that a fee that rises between this read and the send does not bounce
	// the request. Nothing is lost to it: whatever the fee does not take becomes collateral and comes
	// back with loan_result.
	fee := treasuryFees.MustInt(0)
	return new(big.Int).Add(fee, new(big.Int).Div(fee, big.NewInt(10)))
}

func loadAdnlAddress(adnlAddress string) *big.Int {
	adnlAddressBytes, err := hex.DecodeString(adnlAddress)
	if err != nil {
		panic(fmt.Sprintf("Error in decoding adnl address: %v", err))
	}

	adnlAddressBigInt := new(big.Int).SetBytes(adnlAddressBytes)
	return adnlAddressBigInt
}

// loadGlobalID reads the network's global_id (config 19): -239 on mainnet, -3 on testnet. A v5 wallet's
// address depends on it, and the way the treasury address was written says nothing reliable about it.
func loadGlobalID(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt) int32 {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := api.GetBlockchainConfig(ctx, mainchainInfo, 19)
	if err != nil {
		panic(fmt.Sprintf("Error in getting the network's global_id: %v", err))
	}
	return int32(cfg.Get(19).MustBeginParse().MustLoadInt(32))
}

func loadWallet(config Wallet, api ton.APIClientWrapped, globalID int32) *wallet.Wallet {
	var version wallet.VersionConfig
	switch config.Version {
	case "v5r1final":
		// A v5 wallet's address depends on the network it is on.
		version = wallet.ConfigV5R1Final{NetworkGlobalID: globalID}
	case "v4r2":
		version = wallet.V4R2
	case "v3r2":
		version = wallet.V3R2
	default:
		panic(fmt.Sprintf("Error, invalid wallet version, expected v5r1final, v4r2, or v3r2 but got: %v", config.Version))
	}

	secret, err := os.ReadFile(config.Path)
	if err != nil {
		panic(fmt.Sprintf("Error in reading wallet secret: %v", err))
	}

	var w *wallet.Wallet
	switch config.Type {
	case "mnemonic":
		seed := strings.Split(strings.Trim(string(secret), " \n\t"), " ")
		w, err = wallet.FromSeed(api, seed, version)
	case "binary":
		// A 32-byte file is a seed (what mytonctrl writes); a 64-byte one is a full ed25519 private
		// key. Read as a private key, a seed yields a garbage public key and a wallet at the wrong
		// address.
		var key ed25519.PrivateKey
		switch len(secret) {
		case ed25519.SeedSize:
			key = ed25519.NewKeyFromSeed(secret)
		case ed25519.PrivateKeySize:
			key = ed25519.PrivateKey(secret)
		default:
			panic(fmt.Sprintf("Error, a binary wallet secret is %v bytes; expected a %v-byte seed or a %v-byte key",
				len(secret), ed25519.SeedSize, ed25519.PrivateKeySize))
		}
		w, err = wallet.FromPrivateKey(api, key, version)
	default:
		panic(fmt.Sprintf("Error, invalid wallet type, expected mnemonic or binary but got: %v", config.Type))
	}
	if err != nil {
		panic(fmt.Sprintf("Error in loading wallet: %v", err))
	}

	return w
}

func loadLoanAddress(validatorAddress *address.Address, treasuryAddress *address.Address, nextRoundSince uint32,
	api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt) *address.Address {

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	slice := cell.BeginCell().MustStoreAddr(validatorAddress).EndCell().MustBeginParse()

	res, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_loan_address", slice, nextRoundSince)
	if err != nil {
		panic(fmt.Sprintf("Error in getting loan address: %v", err))
	}
	loanAddress := res.MustSlice(0).MustLoadAddr()
	return loanAddress
}

func loadParticipation(participations *cell.Dictionary, nextRoundSince uint32) *Participation {
	participation := Participation{}
	if participations != nil {
		p, err := participations.LoadValueByUintKey(uint64(nextRoundSince))
		switch {
		case err == nil:
			participation = LoadParticipation(p.MustToCell())
		case !errors.Is(err, cell.ErrNoSuchKeyInDict):
			panic(fmt.Sprintf("Error in loading the participation for round %v: %v", nextRoundSince, err))
		}
	}
	return &participation
}

func loadBorrowConfig(config Borrow, minStake *big.Int, maxStakeFactor uint32) (
	*big.Int, *big.Int, *big.Int, *big.Int, uint32) {
	stake, err := tlb.FromTON(config.Stake)
	if err != nil {
		panic("Error, invalid stake amount")
	}

	loan, err := tlb.FromTON(config.Loan)
	if err != nil {
		panic("Error, invalid loan amount")
	}
	if loan.Nano().Cmp(big.NewInt(0)) == 0 {
		loan = tlb.FromNanoTON(minStake)
	}

	minPayment, err := tlb.FromTON(config.MinPayment)
	if err != nil {
		panic("Error, invalid min payment")
	}

	// Absent from a config written before the stake cap: no cap, which is what 0 means on the wire.
	maxStake := big.NewInt(0)
	if config.MaxStake != "" {
		c, err := tlb.FromTON(config.MaxStake)
		if err != nil {
			panic("Error, invalid max stake")
		}
		maxStake = c.Nano()
	}

	// Resolved here, and here only: maxFactor goes into both the confirmation cell the validator
	// engine signs and the new_stake_msg the elector reads, and the elector checks the signature
	// over the first against the second. Anything that adjusts the value downstream of the signing
	// makes the two disagree, which costs the whole round.
	maxFactor, maxFactorNote := resolveMaxFactor(config.MaxFactorRatio, maxStakeFactor)
	if maxFactorNote != "" {
		log.Printf("   %v", maxFactorNote)
	}

	return stake.Nano(), loan.Nano(), minPayment.Nano(), maxStake, maxFactor
}

// buildStakeConfirmation and buildNewStakeMsg both carry max_factor, and the elector verifies the
// signature over the first against the fields of the second. They take it as one argument from one
// caller for that reason: a value adjusted between the two -- clamped on its way into only one of
// them, say -- is a rejected stake and a missed round, and nothing before the elector would say so.

func buildStakeConfirmation(roundSince uint32, maxFactor uint32, loanAddress *address.Address,
	adnlAddress *big.Int) *cell.Cell {
	return cell.BeginCell().
		MustStoreUInt(0x654c5074, 32).
		MustStoreUInt(uint64(roundSince), 32).
		MustStoreUInt(uint64(maxFactor), 32).
		MustStoreBigUInt(new(big.Int).SetBytes(loanAddress.Data()), 256).
		MustStoreBigUInt(adnlAddress, 256).
		EndCell()
}

func buildNewStakeMsg(publicKey []byte, roundSince uint32, maxFactor uint32, adnlAddress *big.Int,
	signature []byte) *cell.Cell {
	return cell.BeginCell().
		MustStoreBigUInt(new(big.Int).SetBytes(publicKey), 256).
		MustStoreUInt(uint64(roundSince), 32).
		MustStoreUInt(uint64(maxFactor), 32).
		MustStoreBigUInt(adnlAddress, 256).
		MustStoreRef(cell.BeginCell().MustStoreSlice(signature, 512).EndCell()).
		EndCell()
}

func loadBalance(w *wallet.Wallet, mainchainInfo *ton.BlockIDExt) *big.Int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	balance, err := w.GetBalance(ctx, mainchainInfo)
	if err != nil {
		panic(fmt.Sprintf("Error in getting wallet balance: %v", err))
	}

	return balance.Nano()
}

func createValidationKey(engine *Engine, nextRoundSince, validatorsElectedFor uint32,
	adnlAddress string) (string, []byte) {
	keyHash := engine.FindPermKeyIfExists(nextRoundSince)

	if keyHash == "" {
		expireAt := nextRoundSince + validatorsElectedFor

		keyHash = engine.NewKey()

		engine.AddPermKey(keyHash, nextRoundSince, expireAt)

		engine.AddTempKey(keyHash, expireAt)

		engine.AddValidatorAddr(keyHash, adnlAddress, expireAt)
	}

	publicKey := engine.ExportPub(keyHash)

	return keyHash, publicKey
}

func sendRequestLoan(w *wallet.Wallet, message *wallet.Message) *tlb.Transaction {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	tx, _, err := w.SendWaitTransaction(ctx, message)
	if err != nil {
		panic(fmt.Sprintf("Error in sending loan request: %v", err))
	}
	return tx
}
