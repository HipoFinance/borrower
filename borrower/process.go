package borrower

import (
	"context"
	"encoding/hex"
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

	participationsList := []*cell.HashmapKV{}
	if participations != nil {
		participationsList = participations.All()
	}

	for _, kv := range participationsList {
		roundSince := uint32(kv.Key.BeginParse().MustLoadUInt(32))
		participation := LoadParticipation(kv.Value)
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

	participations, stopped, borrowerFee, protocolRewardShare := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)

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

	stake, loan, minPayment, maxFactor, rewardShare := loadBorrowConfig(config.Borrow, minStake, maxStakeFactor)

	// When the treasury sets the share, borrow.reward_share is not a bid any more -- it is ignored,
	// and the value below is what every loan in this round will carry. Kept in the comparison so a
	// standing request bid under the old rules is re-sent once rather than left stale.
	protocolSetsShare := protocolRewardShare != nil
	if protocolSetsShare {
		if rewardShare != *protocolRewardShare {
			log.Printf("   ℹ️  The treasury sets reward_share to %v; borrow.reward_share (%v) is ignored",
				*protocolRewardShare, rewardShare)
		}
		rewardShare = *protocolRewardShare
	}

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

	payload := RequestBody(uint64(time.Now().Unix()), nextRoundSince, loan, minPayment, rewardShare,
		protocolSetsShare, newStakeMsg)

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

	// get_treasury_state mirrors the treasury's storage order, and fields get inserted into it
	// rather than appended, so these indices move whenever the layout does. The 24 values are:
	//
	//    0 total_coins             6 parent            12 previous_rate       18 proposed_governor
	//    1 total_tokens            7 participations    13 current_rate        19 governance_fee
	//    2 total_staking           8 rounds_imbalance  14 round_duration      20 borrower_fee
	//    3 total_unstaking         9 stopped?          15 last_settled_round  21 collection_codes
	//    4 total_borrowers_stake  10 instant_mint?     16 halter              22 bill_codes
	//    5 deficit                11 loan_codes        17 governor            23 old_parents
	//
	// A shift shows up as tonutils-go's "incorrect result type" on the first read below, because
	// the value landing at index 7 stops being a cell. That is the loud failure the contract's
	// getter comment promises, and it stops this process before it requests a loan or sends a
	// finish_participation, so both go silent at once. Check the tuple against
	// contract/contracts/treasury.fc's get_treasury_state before assuming the node is at fault.
	//
	// The tuple is append-only from the 2026-09-07 release onward, so its LENGTH now tells this
	// process which treasury it is talking to. A treasury carrying reward_share at index 26 sets
	// the borrower's share itself and refuses a request that still sends one, so that index is
	// read here and the request built to match. One build therefore works on both sides of the
	// upgrade, which matters because the alternative is a window where every request bounces
	// whichever order the two are rolled out in.
	var participations *cell.Dictionary
	if !treasuryState.MustIsNil(7) {
		participations, err = treasuryState.MustCell(7).BeginParse().ToDict(32)
	}
	if err != nil {
		panic(fmt.Sprintf("Error in loading participations dictionary: %v", err))
	}

	stopped := treasuryState.MustInt(9).Cmp(big.NewInt(0)) != 0

	// Index 20, right after governance_fee. Out of 65535 of a borrower's contractual share of the
	// round reward, charged on top of what the pool takes and paid out of the borrower's own funds.
	// Zero disables it entirely, floor included.
	borrowerFee := uint16(treasuryState.MustInt(20).Uint64())

	// nil on a treasury that predates the field, which is the signal to keep sending the share in
	// the request. Present means the protocol sets it and the request must NOT carry one: the
	// handler stops reading it from the message, so an old-format body leaves 16 bits over and
	// throws at end_parse. The message is bounceable, so the collateral comes back, but the round
	// is missed.
	var rewardShare *uint16
	if fields := len(treasuryState.AsTuple()); fields > rewardShareIndex {
		share := uint16(treasuryState.MustInt(rewardShareIndex).Uint64())
		rewardShare = &share
	}

	return participations, stopped, borrowerFee, rewardShare
}

// Index 26, appended by the release that made the share a protocol parameter. Read by length
// rather than assumed, because a treasury older than that release simply does not have it.
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

	// treasuryFees, err := api.RunGetMethod(ctx, mainchainInfo, treasuryAddress, "get_treasury_fees", 0)
	// if err != nil {
	// 	panic(fmt.Sprintf("Error in getting treasury fees: %v", err))
	// }

	// return treasuryFees.MustInt(0)
	return big.NewInt(1000000000)
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

	slice := cell.BeginCell().MustStoreAddr(validatorAddress).EndCell().BeginParse()

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
		p := participations.GetByIntKey(big.NewInt(int64(nextRoundSince)))
		if p != nil {
			participation = LoadParticipation(p)
		}
	}
	return &participation
}

func loadBorrowConfig(config Borrow, minStake *big.Int, maxStakeFactor uint32) (
	*big.Int, *big.Int, *big.Int, uint32, uint16) {
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

	return stake.Nano(), loan.Nano(), minPayment.Nano(), maxFactor, config.RewardShare
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
