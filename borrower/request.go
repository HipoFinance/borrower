package borrower

import (
	"encoding/hex"
	"math/big"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type Request struct {
	MinPayment *big.Int
	// Out of 65535. A bid made on the old 0-255 scale is exactly this value divided by 257.
	RewardShare  uint16
	LoanAmount   *big.Int
	AccrueAmount *big.Int
	StakeAmount  *big.Int
	// The borrower fee in force when the request was made, out of 65535 of the contractual share of
	// the round reward. The treasury snapshots it here rather than reading it at recovery, so a
	// governance change cannot reprice a loan that is already committed.
	BorrowerFee uint16
	// The borrower's cap on loan + accrue + collateral, 0 for none. Requests stored before the
	// treasury's stake-cap release end without it and read as 0, as the treasury reads them.
	MaxStake    *big.Int
	NewStakeMsg *cell.Cell
}

func LoadRequest(c *cell.Cell) Request {
	s := c.MustBeginParse()
	r := Request{
		MinPayment:   s.MustLoadBigCoins(),
		RewardShare:  uint16(s.MustLoadUInt(16)),
		LoanAmount:   s.MustLoadBigCoins(),
		AccrueAmount: s.MustLoadBigCoins(),
		StakeAmount:  s.MustLoadBigCoins(),
		BorrowerFee:  uint16(s.MustLoadUInt(16)),
		MaxStake:     big.NewInt(0),
	}
	if s.BitsLeft() > 0 {
		r.MaxStake = s.MustLoadBigCoins()
	}
	r.NewStakeMsg = s.MustLoadRef().MustToCell()
	return r
}

// OpRequestLoan is op::request_loan in the contract's contracts/imports/constants.fc.
const OpRequestLoan = 0x36335da9

// RequestValue is what a request_loan message must carry. The treasury keeps
// value - request fee as collateral and refuses the request unless that covers
// min_payment + max_punishment, plus fee::min_burn while a borrower fee is in force; any own stake
// rides on top. The punishment is floored at 1 GRAM, as this borrower always has.
func RequestValue(maxPunishment, requestLoanFee, minPayment, stake *big.Int, borrowerFee uint16) *big.Int {
	value := big.NewInt(1000000000)
	if maxPunishment.Cmp(value) == 1 {
		value.Set(maxPunishment)
	}
	value.Add(value, requestLoanFee)
	value.Add(value, minPayment)
	value.Add(value, stake)
	if borrowerFee != 0 {
		// Collateral, not a fee: whatever the burn does not take comes back with loan_result.
		value.Add(value, minBurn)
	}
	return value
}

// RequestBody builds the request_loan body. It carries no reward share: the treasury sets one for
// every loan, and its end_parse refuses a body that still has 16 bits of share before the ref.
//
// maxStake is the field the treasury's stake-cap release requires after min_payment, 0 for no cap.
// Pass nil for a treasury older than that release, which refuses a body carrying it; see
// TakesMaxStake.
func RequestBody(queryID uint64, roundSince uint32, loan, minPayment, maxStake *big.Int,
	newStakeMsg *cell.Cell) *cell.Cell {
	b := cell.BeginCell().
		MustStoreUInt(OpRequestLoan, 32).
		MustStoreUInt(queryID, 64).
		MustStoreUInt(uint64(roundSince), 32).
		MustStoreBigCoins(loan).
		MustStoreBigCoins(minPayment)
	if maxStake != nil {
		b.MustStoreBigCoins(maxStake)
	}
	return b.MustStoreRef(newStakeMsg).EndCell()
}

// preCapTreasuryCodeHashes are the treasury codes before the stake-cap release, which refuse a
// request_loan that carries max_stake: the reward-share release and the accrual-pricing release.
// Every code after them requires it. A treasury older than both is refused elsewhere, for having no
// protocol-set reward share.
var preCapTreasuryCodeHashes = map[string]bool{
	"6cd64455cf733d84a56da540b1ad757e966bdbe8146fe32d52c01efc038a8c6c": true,
	"f003de4b9ab34a61dd7d70a0a68a5faaf6ac0a8821ff2d720f9fecf8dd71475d": true,
}

// TakesMaxStake reports whether a treasury running this code expects max_stake in request_loan.
func TakesMaxStake(codeHash []byte) bool {
	return !preCapTreasuryCodeHashes[hex.EncodeToString(codeHash)]
}

// CapFits reports whether a max_stake is one the treasury accepts for this request: 0 for no cap, or
// at least loan + stake_amount, which it refuses below. held must be an upper bound on that
// stake_amount -- the collateral the request leaves posted plus the fee attached, because the fee is
// attached with slack and what the treasury does not charge of it stays as stake too.
func CapFits(maxStake, loan, held *big.Int) bool {
	return maxStake.Sign() == 0 || maxStake.Cmp(new(big.Int).Add(loan, held)) >= 0
}

// Unchanged reports whether a request already standing in the treasury is the one this borrower
// would send, so that it is not replaced -- each replacement costs another request fee. The share is
// compared too: a request keeps the share it was made under, so one made before the governor changed
// it is re-sent to carry the current value. So is the cap; pass 0 for a treasury that takes none.
func (r Request) Unchanged(loan, minPayment *big.Int, rewardShare uint16, maxStake *big.Int) bool {
	return r.MinPayment.Cmp(minPayment) == 0 && r.RewardShare == rewardShare && r.LoanAmount.Cmp(loan) == 0 &&
		r.MaxStake.Cmp(maxStake) == 0
}

// newStakeConfirmation is fee::new_stake_confirmation in the contract: request_loan refuses unless
// the collateral plus the loan reaches min_stake plus this.
var newStakeConfirmation = big.NewInt(1000000000) // 1 GRAM

// CollateralFor is what the request must leave the treasury holding as stake_amount: RequestValue
// without the fee.
func CollateralFor(maxPunishment, minPayment, stake *big.Int, borrowerFee uint16) *big.Int {
	return RequestValue(maxPunishment, big.NewInt(0), minPayment, stake, borrowerFee)
}

// PunishmentFunc reads the maximum punishment for a stake: get_max_punishment on the treasury.
type PunishmentFunc func(stake *big.Int) *big.Int

// SizeRequest works out the collateral a request needs. request_loan checks the punishment on
// loan + collateral, not on the loan alone, and the collateral itself includes the punishment, so a
// punishment that grows with the stake is read a second time on the stake it would actually check.
// Punishment is a flat fine today, which makes the second read a no-op.
func SizeRequest(loan, minPayment, stake *big.Int, borrowerFee uint16, punishment PunishmentFunc) (
	collateral, maxPunishment *big.Int) {
	maxPunishment = punishment(loan)
	collateral = CollateralFor(maxPunishment, minPayment, stake, borrowerFee)
	if p := punishment(new(big.Int).Add(loan, collateral)); p.Cmp(maxPunishment) > 0 {
		maxPunishment = p
		collateral = CollateralFor(maxPunishment, minPayment, stake, borrowerFee)
	}
	return collateral, maxPunishment
}

// CoversMinStake reports whether request_loan's stake_amount + loan_amount >= min_stake +
// fee::new_stake_confirmation holds for this collateral.
func CoversMinStake(collateral, loan, minStake *big.Int) bool {
	have := new(big.Int).Add(collateral, loan)
	return have.Cmp(new(big.Int).Add(minStake, newStakeConfirmation)) >= 0
}

// SendValue is what to attach to request_loan. The treasury keeps incoming - fee + the collateral
// already posted by a request it replaces, so a replacement only sends the fee and whatever the
// posted collateral falls short of; a new request sends the whole collateral.
func SendValue(collateral, requestLoanFee, posted *big.Int) *big.Int {
	value := new(big.Int).Set(requestLoanFee)
	short := new(big.Int).Sub(collateral, posted)
	if short.Sign() > 0 {
		value.Add(value, short)
	}
	return value
}
