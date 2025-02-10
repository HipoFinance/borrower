package borrower

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
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
		}
	}()

	config := loadConfig()

	api, ctx := loadApi(config)

	treasuryAddress := address.MustParseAddr(config.Treasury)

	mainchainInfo := loadMainchainInfo(api, ctx)

	validatorsElectedFor, _, currentVsetHash, nextRoundSince, _ := loadBlockchainConfig(api, ctx, mainchainInfo)

	participations, _ := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)

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

		if participation.State == ParticipationOpen {
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

		} else if participation.State == ParticipationDistributing {
			next := 30 * time.Second
			if wait == 0 || wait > next {
				wait = next
			}

		} else if participation.State == ParticipationStaked {
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

		} else if participation.State == ParticipationValidating {
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

		} else if participation.State == ParticipationHeld {
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

func RequestLoan() (wait time.Duration) {
	defer func() {
		if err := recover(); err != nil {
			wait = 0
			log.Printf("   ❌ %s", err)
		}
	}()

	config := loadConfig()

	api, ctx := loadApi(config)

	engine := NewValidatorEngine(config.ValidatorEngine)

	treasuryAddress := address.MustParseAddr(config.Treasury)

	checkLiteserverIsSync(engine)

	mainchainInfo := loadMainchainInfo(api, ctx)

	validatorsElectedFor, minStake, _, nextRoundSince, stakeHeldFor := loadBlockchainConfig(api, ctx, mainchainInfo)

	participations, stopped := loadTreasuryState(api, ctx, mainchainInfo, treasuryAddress)

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

	stake, loan, minPayment, maxFactor, validatorRewardShare, mevRewardShare := loadBorrowConfig(config.Borrow, minStake)

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
		if r.MinPayment.Cmp(minPayment) == 0 &&
			r.ValidatorRewardShare == validatorRewardShare &&
			r.LoanAmount.Cmp(loan) == 0 {
			log.Printf("   ⏩ Already participated in round %v", formattedNextRoundSince)
			return
		} else {
			log.Printf("   ✏️  Updating last request to min_payment: %v, validator_reward_share: %v, loan: %v",
				minPayment, validatorRewardShare, loan)
		}
	}
	if participation.State != ParticipationOpen {
		log.Printf("   ⏩ Loan requests are not accepted at the moment for round %v", formattedNextRoundSince)
		return
	}

	value := big.NewInt(1000000000)
	if maxPunishment.Cmp(value) == 1 {
		value = maxPunishment
	}
	value = value.Add(value, requestLoanFee)
	value = value.Add(value, minPayment)
	value = value.Add(value, stake)

	balance := loadBalance(w, mainchainInfo)
	if balance.Cmp(value) != 1 {
		log.Printf("   ⚠️  Low balance, need at least %v TON, but your wallet balance is %v TON",
			tlb.FromNanoTON(value).String(), tlb.FromNanoTON(balance).String())
		return 0
	}

	log.Printf("   🛠  Configuring validator engine for round %v", formattedNextRoundSince)

	keyHash, publicKey :=
		createValidationKey(engine, nextRoundSince, validatorsElectedFor, config.ValidatorEngine.AdnlAddress)

	log.Printf("   💎 Requesting a loan of %v TON, sending %v TON, for validation round %v",
		tlb.FromNanoTON(loan).String(), tlb.FromNanoTON(value), formattedNextRoundSince)

	confirmation := cell.BeginCell().
		MustStoreUInt(0x654c5074, 32).
		MustStoreUInt(uint64(nextRoundSince), 32).
		MustStoreUInt(uint64(maxFactor), 32).
		MustStoreBigUInt(new(big.Int).SetBytes(loanAddress.Data()), 256).
		MustStoreBigUInt(adnlAddressBigInt, 256).
		EndCell()

	signature := engine.Sign(keyHash, confirmation)

	newStakeMsg := cell.BeginCell().
		MustStoreBigUInt(new(big.Int).SetBytes(publicKey), 256).
		MustStoreUInt(uint64(nextRoundSince), 32).
		MustStoreUInt(uint64(maxFactor), 32).
		MustStoreBigUInt(adnlAddressBigInt, 256).
		MustStoreRef(cell.BeginCell().MustStoreSlice(signature, 512).EndCell()).
		EndCell()

	payload := cell.BeginCell().
		MustStoreUInt(0x36335da9, 32).
		MustStoreUInt(uint64(time.Now().Unix()), 64).
		MustStoreUInt(uint64(nextRoundSince), 32).
		MustStoreBigCoins(loan).
		MustStoreBigCoins(minPayment).
		MustStoreUInt(uint64(validatorRewardShare), 14).
		MustStoreUInt(uint64(mevRewardShare), 14).
		MustStoreRef(newStakeMsg).
		EndCell()

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

func loadApi(config *Config) (ton.APIClientWrapped, context.Context) {
	client := liteclient.NewConnectionPool()
	err := client.AddConnectionsFromConfigFile(config.GlobalConfig)
	if err != nil {
		panic(fmt.Sprintf("Error in loading global config: %v", err))
	}

	ctx := client.StickyContext(context.Background())

	api := ton.NewAPIClient(client).WithRetry(10)
	return api, ctx
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
	uint32, *big.Int, *big.Int, uint32, uint32) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	blockchainConfig, err :=
		api.GetBlockchainConfig(ctx, mainchainInfo, ConfigElection, ConfigCurrentValidators, ConfigStake)
	if err != nil {
		panic(fmt.Sprintf("Error in getting blockchain config: %v", err))
	}

	validatorsElectedFor, _, _, stakeHeldFor := GetElectionConfig(blockchainConfig.Get(ConfigElection))
	minStake := GetMinStake(blockchainConfig.Get(ConfigStake))
	currentValidators := blockchainConfig.Get(ConfigCurrentValidators)
	currentVsetHash := new(big.Int).SetBytes(currentValidators.Hash())
	_, nextRoundSince := GetVsetTimes(currentValidators)

	return validatorsElectedFor, minStake, currentVsetHash, nextRoundSince, stakeHeldFor
}

func loadTreasuryState(api ton.APIClientWrapped, ctx context.Context, mainchainInfo *ton.BlockIDExt,
	treasuryAddress *address.Address) (*cell.Dictionary, bool) {

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

	var participations *cell.Dictionary
	if !treasuryState.MustIsNil(6) {
		participations, err = treasuryState.MustCell(6).BeginParse().ToDict(32)
	}
	if err != nil {
		panic(fmt.Sprintf("Error in loading participations dictionary: %v", err))
	}

	stopped := treasuryState.MustInt(8).Cmp(big.NewInt(0)) != 0

	return participations, stopped
}

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
	var version wallet.Version
	if config.Version == "v4r2" {
		version = wallet.V4R2
	} else if config.Version == "v3r2" {
		version = wallet.V3R2
	} else {
		panic(fmt.Sprintf("Error, invalid wallet version, expected v4r2 or v3r2 but got: %v", config.Version))
	}

	secret, err := os.ReadFile(config.Path)
	if err != nil {
		panic(fmt.Sprintf("Error in reading wallet secret: %v", err))
	}

	var w *wallet.Wallet
	if config.Type == "mnemonic" {
		seed := strings.Split(strings.Trim(string(secret), " \n\t"), " ")
		w, err = wallet.FromSeed(api, seed, version)
	} else if config.Type == "binary" {
		w, err = wallet.FromPrivateKey(api, secret, version)
	} else {
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

func loadBorrowConfig(config Borrow, minStake *big.Int) (*big.Int, *big.Int, *big.Int, uint32, uint16, uint16) {
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

	if config.MaxFactorRatio < 1 {
		panic("Error, max_factor_ratio must be >= 1.0")
	}
	maxFactor := uint32(config.MaxFactorRatio * 65536)

	// validate config.MevRewardShare must be 0-10000
	if config.MevRewardShare > 10000 {
		panic("Error, mev_reward_share must be <= 10000")
	}

	// validate config.ValidatorRewardShare must be 0-10000
	if config.ValidatorRewardShare > 10000 {
		panic("Error, validator_reward_share must be <= 10000")
	}

	return stake.Nano(), loan.Nano(), minPayment.Nano(), maxFactor, config.ValidatorRewardShare, config.MevRewardShare
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
