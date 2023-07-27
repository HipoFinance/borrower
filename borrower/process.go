package borrower

import (
	"context"
	"encoding/base64"
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

func Process() {
	config, err := ReadConfig()
	if err != nil {
		log.Printf("❌ Error in reading borrower.yaml: %v", err)
		return
	}

	liteserverKeyContent, err := os.ReadFile(config.ValidatorEngine.LiteserverKey)
	if err != nil {
		log.Printf("❌ Error in reading liteserver_key: %v", err)
		return
	}
	liteserverKey := base64.StdEncoding.EncodeToString(liteserverKeyContent[len(liteserverKeyContent)-32:])

	client := liteclient.NewConnectionPool()
	ctx := client.StickyContext(context.Background())
	liteserverAddress := fmt.Sprintf("%v:%v", config.ValidatorEngine.Ip, config.ValidatorEngine.LiteserverPort)
	err = client.AddConnection(ctx, liteserverAddress, liteserverKey)
	if err != nil {
		log.Printf("❌ Error in connecting to liteserver: %v", err)
		return
	}

	tonlib := NewTonlib(config.TonlibCli.Executable, config.TonlibCli.GlobalConfig)
	console := NewValidatorEngine(config.ValidatorEngine)
	timedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	isSync := console.IsSync(timedCtx)

	if !isSync {
		log.Printf("❌ Error, liteserver is out of sync")
		return
	}

	api := ton.NewAPIClient(client)
	masterchainInfo, err := api.CurrentMasterchainInfo(ctx)
	if err != nil {
		log.Printf("❌ Error in getting current masterchain info: %v", err)
		return
	}

	blockchainConfig, err := api.GetBlockchainConfig(ctx, masterchainInfo, ConfigCurrentValidators, ConfigStake)
	if err != nil {
		log.Printf("❌ Error in getting blockchain config: %v", err)
		return
	}

	minStake := GetMinStake(blockchainConfig.Get(ConfigStake))
	currentValidators := blockchainConfig.Get(ConfigCurrentValidators)
	currentVsetHash := new(big.Int).SetBytes(currentValidators.Hash())
	_, nextRoundSince := GetVsetTimes(currentValidators)

	treasuryAddress := address.MustParseAddr(config.Treasury)
	treasuryAccount, err := api.GetAccount(ctx, masterchainInfo, treasuryAddress)
	if err != nil {
		log.Printf("❌ Error in getting treasury account: %v", err)
		return
	}
	if !treasuryAccount.IsActive {
		log.Printf("❌ Error, treasury account is not active")
		return
	}

	treasuryState, err := api.RunGetMethod(ctx, masterchainInfo, treasuryAddress, "get_treasury_state")
	if err != nil {
		log.Printf("❌ Error in getting treasury state: %v", err)
		return
	}

	participateSince, err := tonlib.GetParticipateSince(timedCtx, *treasuryAddress)
	if err != nil {
		log.Printf("❌ Error in getting times: %v", err)
		return
	}

	var participations *cell.Dictionary
	if !treasuryState.MustIsNil(5) {
		participations, err = treasuryState.MustCell(5).BeginParse().ToDict(32)
	}
	if err != nil {
		log.Printf("❌ Error in loading participations dictionary: %v", err)
		return
	}

	participationsList := []*cell.HashmapKV{}
	if participations != nil {
		participationsList = participations.All()
	}

	for _, kv := range participationsList {
		roundSince := kv.Key.BeginParse().MustLoadUInt(32)
		participation := LoadParticipation(kv.Value)

		if participation.State == ParticipationOpen {
			if participateSince < int(time.Now().Unix()) {
				err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
					DstAddr: treasuryAddress,
					Body: cell.BeginCell().
						MustStoreUInt(0x574a297b, 32).
						MustStoreUInt(uint64(time.Now().Unix()), 64).
						MustStoreUInt(roundSince, 32).
						EndCell(),
				})
				if err != nil {
					log.Printf("❌ Error in sending participate_in_election: %v", err)
				} else {
					log.Printf("☑️ Sent participate_in_election")
				}
			}
		} else if (participation.State == ParticipationStaked || participation.State == ParticipationValidating) &&
			participation.CurrentVsetHash != currentVsetHash {
			err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
				DstAddr: treasuryAddress,
				Body: cell.BeginCell().
					MustStoreUInt(0x2f0b5b3b, 32).
					MustStoreUInt(uint64(time.Now().Unix()), 64).
					MustStoreUInt(roundSince, 32).
					EndCell(),
			})
			if err != nil {
				log.Printf("❌ Error in sending vset_changed: %v", err)
			} else {
				log.Printf("☑️ Sent vset_changed")
			}
		} else if participation.State == ParticipationHeld && uint32(time.Now().Unix()) > participation.StakeHeldUntil {
			err := api.SendExternalMessage(ctx, &tlb.ExternalMessage{
				DstAddr: treasuryAddress,
				Body: cell.BeginCell().
					MustStoreUInt(0x23274435, 32).
					MustStoreUInt(uint64(time.Now().Unix()), 64).
					MustStoreUInt(roundSince, 32).
					EndCell(),
			})
			if err != nil {
				log.Printf("❌ Error in sending finish_participation: %v", err)
			} else {
				log.Printf("☑️ Sent finish_participation")
			}
		}
	}

	if !config.Borrow.Active {
		log.Printf("↩️  Borrowing is inactive")
		return
	}

	if config.Borrow.MaxFactorRatio < 1 {
		log.Printf("❌ Error, max_factor_ratio must be >= 1.0")
		return
	}
	maxFactor := uint64(config.Borrow.MaxFactorRatio * 65536)

	stopped := treasuryState.MustInt(6)
	if stopped.Cmp(big.NewInt(0)) != 0 {
		log.Printf("🔲 Treasury is stopped")
		return
	}

	var version wallet.Version
	if config.Wallet.Version == "v4r2" {
		version = wallet.V4R2
	} else if config.Wallet.Version == "v3r2" {
		version = wallet.V3R2
	} else {
		log.Printf("❌ Error, invalid wallet version, expected v4r2 or v3r2 but got: %v", config.Wallet.Version)
		return
	}

	secret, err := os.ReadFile(config.Wallet.Path)
	if err != nil {
		log.Printf("❌ Error in reading wallet secret: %v", err)
		return
	}

	var w *wallet.Wallet
	if config.Wallet.Type == "mnemonic" {
		seed := strings.Split(strings.Trim(string(secret), " \n\t"), " ")
		w, err = wallet.FromSeed(api, seed, version)
	} else if config.Wallet.Type == "binary" {
		w, err = wallet.FromPrivateKey(api, secret, version)
	} else {
		log.Printf("❌ Error, invalid wallet type, expected mnemonic or binary but got: %v", config.Wallet.Type)
		return
	}
	if err != nil {
		log.Printf("❌ Error in loading wallet: %v", err)
		return
	}

	validatorAddress := w.Address()
	validatorAddress.SetTestnetOnly(treasuryAddress.IsTestnetOnly())
	validatorAddressSlice := cell.BeginCell().MustStoreAddr(validatorAddress).EndCell().BeginParse()

	addrRes, err := api.RunGetMethod(ctx, masterchainInfo, treasuryAddress, "get_loan_address", validatorAddressSlice, nextRoundSince)
	if err != nil {
		log.Printf("❌ Error in getting loan address: %v", err)
		return
	}
	loanAddress := addrRes.MustSlice(0).MustLoadAddr()
	log.Printf("Loan Address: %v", loanAddress)

	adnlAddressBytes, err := hex.DecodeString(config.ValidatorEngine.AdnlAddress)
	if err != nil {
		log.Printf("❌ Error in decoding adnl address: %v", err)
		return
	}
	adnlAddressBigInt := new(big.Int).SetBytes(adnlAddressBytes)

	participation := Participation{}
	if participations != nil {
		p := participations.GetByIntKey(big.NewInt(int64(nextRoundSince)))
		if p != nil {
			participation = LoadParticipation(p)
		}
	}

	key := cell.BeginCell().MustStoreBigUInt(new(big.Int).SetBytes(validatorAddress.Data()), 256).EndCell()
	if participation.Requests != nil && participation.Requests.Get(key) != nil {
		log.Printf("⏩ Already participated, skipping")
		return
	}

	if participation.State != ParticipationOpen {
		log.Printf("⏩ Loan requests are not acceted, skipping")
		return
	}

	loan, err := tlb.FromTON(config.Borrow.Loan)
	if err != nil {
		log.Printf("❌ Error, invalid loan amount")
		return
	}
	if loan.NanoTON().Cmp(big.NewInt(0)) == 0 {
		loan = tlb.FromNanoTON(minStake)
	}

	punishRes, err := api.RunGetMethod(ctx, masterchainInfo, treasuryAddress, "get_max_punishment", loan.NanoTON())
	if err != nil {
		log.Printf("❌ Error in getting max punishment: %v", err)
		return
	}
	maxPunishment := punishRes.MustInt(0)

	requestLoanFee, err := tonlib.GetRequestLoadFee(timedCtx, *treasuryAddress)
	if err != nil {
		log.Printf("❌ Error in getting fees: %v", err)
		return
	}

	stake, err := tlb.FromTON(config.Borrow.Stake)
	if err != nil {
		log.Printf("❌ Error, invalid stake amount")
		return
	}

	minPayment, err := tlb.FromTON(config.Borrow.MinPayment)
	if err != nil {
		log.Printf("❌ Error, invalid min payment")
		return
	}

	value := big.NewInt(1000000000)
	value = value.Add(value, requestLoanFee)
	value = value.Add(value, maxPunishment)
	value = value.Add(value, stake.NanoTON())
	value = value.Add(value, minPayment.NanoTON())

	balance, err := w.GetBalance(ctx, masterchainInfo)
	if err != nil {
		log.Printf("❌ Error in getting wallet balance: %v", err)
		return
	}

	if balance.NanoTON().Cmp(value) != 1 {
		log.Printf("⚠️ Low balance, need at least %v TON, but your wallet balance is %v TON",
			tlb.FromNanoTON(value), balance.TON())
		return
	}

	log.Printf("💎 Requesting a loan of: %v TON, at round: %v, sending %v TON",
		loan.TON(), time.Unix(int64(nextRoundSince), 0), tlb.FromNanoTON(value))

	expireAt := nextRoundSince + 86400

	keyHash := console.NewKey(timedCtx)
	if keyHash == "" {
		log.Printf("❌ Error in creating new key")
		return
	}

	addedPermKey := console.AddPermKey(timedCtx, keyHash, nextRoundSince, expireAt)
	if addedPermKey != "success" {
		log.Printf("❌ Error in adding permanent key")
		return
	}

	addedTempKey := console.AddTempKey(timedCtx, keyHash, expireAt)
	if addedTempKey != "success" {
		log.Printf("❌ Error in adding temporary key")
		return
	}

	addedValidatorAddr := console.AddValidatorAddr(timedCtx, keyHash, config.ValidatorEngine.AdnlAddress, expireAt)
	if addedValidatorAddr != "success" {
		log.Printf("❌ Error in adding validator address")
		return
	}

	publicKey := console.ExportPub(timedCtx, keyHash)
	if publicKey == "" {
		log.Printf("❌ Error in exporting public key")
		return
	}

	newStakeMsg := cell.BeginCell().
		MustStoreUInt(0x654c5074, 32).
		MustStoreUInt(uint64(nextRoundSince), 32).
		MustStoreUInt(maxFactor, 32).
		MustStoreBigUInt(new(big.Int).SetBytes(loanAddress.Data()), 256).
		MustStoreBigUInt(adnlAddressBigInt, 256).
		EndCell()

	signature := console.Sign(timedCtx, keyHash, newStakeMsg)
	if signature == "" {
		log.Printf("❌ Error in signing loan request")
		return
	}

	payload := cell.BeginCell().
		MustStoreUInt(0x12b808d3, 32).
		MustStoreUInt(uint64(time.Now().Unix()), 64).
		MustStoreUInt(uint64(nextRoundSince), 32).
		MustStoreBigCoins(loan.NanoTON()).
		MustStoreBigCoins(minPayment.NanoTON()).
		MustStoreUInt(uint64(config.Borrow.ValidatorRewardShare), 8).
		MustStoreRef(newStakeMsg).
		EndCell()
	message := wallet.SimpleMessage(treasuryAddress, tlb.FromNanoTON(value), payload)

	tx, _, err := w.SendWaitTransaction(ctx, message)
	if err != nil {
		log.Printf("❌ Error in sending loan request: %v", err)
		return
	}

	log.Printf("✅ Sent %v", tx)
}
