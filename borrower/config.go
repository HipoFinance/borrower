package borrower

import (
	"fmt"
	"math/big"
	"os"

	"github.com/xssnick/tonutils-go/tvm/cell"
	"gopkg.in/yaml.v3"
)

var ConfigFile = "borrower.yaml"

type Config struct {
	Treasury        string
	GlobalConfig    string `yaml:"global_config"`
	Borrow          Borrow
	Wallet          Wallet
	ValidatorEngine ValidatorEngine `yaml:"validator_engine"`
}

type Borrow struct {
	Active         bool
	Stake          string
	Loan           string
	MinPayment     string  `yaml:"min_payment"`
	MaxFactorRatio float32 `yaml:"max_factor_ratio"`

	// There is no reward share here. The treasury sets one for every loan and refuses a request that
	// names its own, so it is read from get_treasury_state. A config that still carries the old
	// reward_share or validator_reward_share key loads fine: unknown keys are ignored.
}

type Wallet struct {
	Type    string
	Path    string
	Version string
}

type ValidatorEngine struct {
	Executable     string
	ClientKey      string `yaml:"client_key"`
	ServerKey      string `yaml:"server_key"`
	LiteserverKey  string `yaml:"liteserver_key"`
	Ip             string `yaml:"ip"`
	ControlPort    uint16 `yaml:"control_port"`
	LiteserverPort uint16 `yaml:"liteserver_port"`
	AdnlAddress    string `yaml:"adnl_address"`
}

func ReadConfig() (config *Config, err error) {
	contents, err := os.ReadFile(ConfigFile)
	if err != nil {
		return
	}

	err = yaml.Unmarshal(contents, &config)
	return
}

// fee::min_burn in contracts/imports/constants.fc: the smallest burn a borrower fee can produce, so
// that every accepted loan burns something even when the borrower contracted for no reward. The
// treasury requires collateral to cover it, and it is not exposed by any getter, so it is mirrored
// here. Collateral, not a fee -- whatever the burn does not take is returned with loan_result.
var minBurn = big.NewInt(1000000000) // 1 GRAM

var ConfigElection int32 = 15
var ConfigStake int32 = 17
var ConfigCurrentValidators int32 = 34

func GetElectionConfig(c *cell.Cell) (uint32, uint32, uint32, uint32) {
	// _ validators_elected_for:uint32 elections_start_before:uint32
	//   elections_end_before:uint32 stake_held_for:uint32
	//   = ConfigParam 15;
	s := c.MustBeginParse()
	return uint32(s.MustLoadUInt(32)), uint32(s.MustLoadUInt(32)),
		uint32(s.MustLoadUInt(32)), uint32(s.MustLoadUInt(32))
}

func GetMinStake(c *cell.Cell) *big.Int {
	// _ min_stake:Grams max_stake:Grams min_total_stake:Grams max_stake_factor:uint32 = ConfigParam 17;
	s := c.MustBeginParse()
	return s.MustLoadBigCoins()
}

// GetMaxStakeFactor reads the last field of the same cell GetMinStake reads: the largest max_factor
// the network will honour, out of 65536.
func GetMaxStakeFactor(c *cell.Cell) uint32 {
	// _ min_stake:Grams max_stake:Grams min_total_stake:Grams max_stake_factor:uint32 = ConfigParam 17;
	s := c.MustBeginParse()
	s.MustLoadBigCoins() // min_stake
	s.MustLoadBigCoins() // max_stake
	s.MustLoadBigCoins() // min_total_stake
	return uint32(s.MustLoadUInt(32))
}

// FactorRatio renders a max_factor as the ratio an operator writes in the config.
func FactorRatio(factor uint32) float32 {
	return float32(factor) / 65536
}

// resolveMaxFactor turns the configured ratio into the max_factor that goes into new_stake, along
// with the line to log about it, if there is one.
//
// Zero means "whatever the network allows", the same convention `loan` uses for min_stake. It is
// the default because this value mirrors a network parameter, and a copy of a network parameter is
// a stale value waiting to happen: the config shipped 3.0, the network moved to 4.5, and nothing
// read the difference for months while a third of every capped loan sat idle at the elector.
//
// A configured value below the network maximum is still honoured, because the factor is how an
// operator bounds their share of block production -- the cap is relative to the smallest elected
// validator, so it limits weight, and weight is what decides shard duty. Above the maximum it is
// clamped rather than sent: whether the elector clamps an over-range factor or rejects the
// new_stake outright was never established, and a rejected stake costs a whole round.
//
// The comparison is done on the 65536 scale rather than on the ratios, because max_factor_ratio is
// a float32 and a ratio that is not exactly representable would otherwise decide the clamp on
// rounding noise.
func resolveMaxFactor(ratio float32, networkMax uint32) (uint32, string) {
	if ratio == 0 {
		return networkMax, ""
	}
	if ratio < 1 {
		panic("Error, max_factor_ratio must be 0 or >= 1.0")
	}

	configured := uint32(ratio * 65536)
	switch {
	case configured > networkMax:
		return networkMax, fmt.Sprintf(
			"ℹ️  max_factor_ratio %v exceeds the network maximum of %v, bidding with %v",
			ratio, FactorRatio(networkMax), FactorRatio(networkMax))
	case configured < networkMax:
		return configured, fmt.Sprintf(
			"⚠️  max_factor_ratio %v is below the network maximum of %v, capping your effective "+
				"stake at %v times the smallest elected stake",
			ratio, FactorRatio(networkMax), ratio)
	default:
		return configured, ""
	}
}

func GetVsetTimes(c *cell.Cell) (since uint32, until uint32) {
	// validators_ext#12 utime_since:uint32 utime_until:uint32
	//   total:(## 16) main:(## 16) { main <= total } { main >= 1 }
	//   total_weight:uint64 list:(HashmapE 16 ValidatorDescr) = ValidatorSet;
	s := c.MustBeginParse()
	if s.MustLoadUInt(8) != 0x12 {
		panic("Unexpected validators_ext")
	}
	since = uint32(s.MustLoadUInt(32))
	until = uint32(s.MustLoadUInt(32))
	return
}
