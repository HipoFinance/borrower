package borrower

import (
	"context"
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

	participations, stopped, borrowerFee, rewardShare := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)

	formattedNextRoundSince := time.Unix(int64(nextRoundSince), 0).Format(TimeFormat)

	wait = time.Until(time.Unix(int64(nextRoundSince+stakeHeldFor+60), 0))

	if !config.Borrow.Active {
		log.Printf("   ↩️  Borrow config is inactive")
		return 0
	}

	adnlAddressBigInt := loadAdnlAddress(config.ValidatorEngine.AdnlAddress)

	w := loadWallet(config.Wallet, api)

	validatorAddress := w.Address()
	validatorAddress.SetTestnetOnly(treasuryAddress.IsTestnetOnly())
	validatorKey := cell.BeginCell().MustStoreBigUInt(new(big.Int).SetBytes(validatorAddress.Data()), 256).EndCell()

	loanAddress := loadLoanAddress(validatorAddress, treasuryAddress, nextRoundSince, api, ctx, mainchainInfo)

	stake, loan, minPayment, maxFactor := loadBorrowConfig(config.Borrow, minStake, maxStakeFactor)

	maxPunishment := getMaxPunishment(api, ctx, mainchainInfo, treasuryAddress, loan)

	requestLoanFee := getRequestLoanFee(api, ctx, mainchainInfo, treasuryAddress)

	if stopped {
		log.Printf("   🔲 Treasury is stopped")
		return 0
	}

	participation := loadParticipation(participations, nextRoundSince)
	if participation.Requests != nil && participation.Requests.Get(validatorKey) != nil {
		cell := participation.Requests.Get(validatorKey)
		r := LoadRequest(cell)
		if r.Unchanged(loan, minPayment, rewardShare) {
			log.Printf("   ⏩ Already participated in round %v", formattedNextRoundSince)
			return
		} else {
			log.Printf("   ✏️  Updating last request to min_payment: %v, reward_share: %v, loan: %v",
				minPayment, rewardShare, loan)
		}
	}
	if participation.State != ParticipationOpen {
		log.Printf("   ⏩ Loan requests are not accepted at the moment for round %v", formattedNextRoundSince)
		return
	}

	value := RequestValue(maxPunishment, requestLoanFee, minPayment, stake, borrowerFee)

	balance := loadBalance(w, mainchainInfo)
	if balance.Cmp(value) != 1 {
		log.Printf("   ⚠️  Low balance, need at least %v GRAM, but your wallet balance is %v GRAM",
			tlb.FromNanoTON(value).String(), tlb.FromNanoTON(balance).String())
		return 0
	}

	if DryRun {
		log.Printf("   🧪 Dry run: would request a loan of %v GRAM at min_payment %v, sending %v GRAM, "+
			"for validation round %v", tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(minPayment).String(),
			tlb.FromNanoTON(value).String(), formattedNextRoundSince)
		log.Printf("   🧪 Bid: %v", BidRate(minPayment, loan))
		log.Printf("   🧪 Stopping before the validator engine and the wallet are touched")
		return
	}

	log.Printf("   🛠  Configuring validator engine for round %v", formattedNextRoundSince)

	keyHash, publicKey :=
		createValidationKey(engine, nextRoundSince, validatorsElectedFor, config.ValidatorEngine.AdnlAddress)

	log.Printf("   💎 Requesting a loan of %v GRAM, sending %v GRAM, for validation round %v",
		tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(value), formattedNextRoundSince)
	// The treasury scales min_payment to everything the loan stakes, leftover included, so the rate
	// is what this bid promises -- see "Pricing a bid" in the README.
	log.Printf("   🏷  Bid: %v; any leftover the treasury adds to this loan is charged at the same rate",
		BidRate(minPayment, loan))

	confirmation := buildStakeConfirmation(nextRoundSince, maxFactor, loanAddress, adnlAddressBigInt)

	signature := engine.Sign(keyHash, confirmation)

	newStakeMsg := buildNewStakeMsg(publicKey, nextRoundSince, maxFactor, adnlAddressBigInt, signature)

	payload := RequestBody(uint64(time.Now().Unix()), nextRoundSince, loan, minPayment, newStakeMsg)

	message := wallet.SimpleMessage(treasuryAddress, tlb.FromNanoTON(value), payload)

	sendRequestLoan(w, message)

	log.Printf("   ✅ Sent a loan request for round %v", formattedNextRoundSince)

	return
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
	treasuryAddress *address.Address) (*cell.Dictionary, bool, uint16, uint16) {

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

	// get_treasury_state mirrors the treasury's storage order. The 28 values are:
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
	// release. A treasury without it still expects the share in the request, which this borrower no
	// longer sends, so it is refused here rather than bidding in a format that would bounce.
	if fields := len(treasuryState.AsTuple()); fields <= rewardShareIndex {
		panic(fmt.Sprintf("Error, the treasury returns %v values from get_treasury_state and has no "+
			"protocol-set reward share; this borrower needs a treasury from 2026-09-21 or later", fields))
	}
	rewardShare := uint16(treasuryState.MustInt(rewardShareIndex).Uint64())

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

func loadWallet(config Wallet, api ton.APIClientWrapped) *wallet.Wallet {
	var version wallet.VersionConfig
	switch config.Version {
	case "v5r1final":
		version = wallet.ConfigV5R1Final{NetworkGlobalID: wallet.MainnetGlobalID}
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
		w, err = wallet.FromPrivateKey(api, secret, version)
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
	*big.Int, *big.Int, *big.Int, uint32) {
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

	// Resolved here, and here only: maxFactor goes into both the confirmation cell the validator
	// engine signs and the new_stake_msg the elector reads, and the elector checks the signature
	// over the first against the second. Anything that adjusts the value downstream of the signing
	// makes the two disagree, which costs the whole round.
	maxFactor, maxFactorNote := resolveMaxFactor(config.MaxFactorRatio, maxStakeFactor)
	if maxFactorNote != "" {
		log.Printf("   %v", maxFactorNote)
	}

	return stake.Nano(), loan.Nano(), minPayment.Nano(), maxFactor
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

func sendRequestLoan(w *wallet.Wallet, message *wallet.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, _, err := w.SendWaitTransaction(ctx, message)
	if err != nil {
		panic(fmt.Sprintf("Error in sending loan request: %v", err))
	}
}
