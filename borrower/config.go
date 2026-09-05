package borrower

import (
	"errors"
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

	// Out of 65535, not 255. The treasury widened this field so that validators can compete in fine
	// steps: on the old scale one step moved a validator's own take by 1/share, which at the shares
	// actually bid was over 12%, and at the bottom of the range the only move left was to zero.
	RewardShare uint16 `yaml:"reward_share"`

	// The old key, kept only to refuse a config that was not migrated. A value written for the 0-255
	// scale is a perfectly valid number on the 0-65535 one, so nothing would complain -- it would just
	// quietly hand the pool almost the entire reward. Read as a pointer so that "absent" and "zero"
	// are distinguishable.
	LegacyValidatorRewardShare *uint16 `yaml:"validator_reward_share"`
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
	if err != nil {
		return
	}

	err = config.validate()
	return
}

func (config *Config) validate() error {
	if config.Borrow.LegacyValidatorRewardShare != nil {
		return errors.New("borrow.validator_reward_share has been replaced by borrow.reward_share, " +
			"which is out of 65535 rather than 255. Multiply your old value by 257 and rename the key: " +
			"8 becomes 2056, 102 becomes 26214. The old value would still parse on the new scale and " +
			"would give away almost your whole share, so it is refused rather than converted")
	}

	if config.Borrow.Active && config.Borrow.RewardShare == 0 {
		return errors.New("borrow.reward_share is 0, which contracts you to earn nothing from a round. " +
			"Set it out of 65535: 2056 is about 3.1%, 26214 is 40%")
	}

	return nil
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
	s := c.BeginParse()
	return uint32(s.MustLoadUInt(32)), uint32(s.MustLoadUInt(32)),
		uint32(s.MustLoadUInt(32)), uint32(s.MustLoadUInt(32))
}

func GetMinStake(c *cell.Cell) *big.Int {
	// _ min_stake:Grams max_stake:Grams min_total_stake:Grams max_stake_factor:uint32 = ConfigParam 17;
	s := c.BeginParse()
	return s.MustLoadBigCoins()
}

func GetVsetTimes(c *cell.Cell) (since uint32, until uint32) {
	// validators_ext#12 utime_since:uint32 utime_until:uint32
	//   total:(## 16) main:(## 16) { main <= total } { main >= 1 }
	//   total_weight:uint64 list:(HashmapE 16 ValidatorDescr) = ValidatorSet;
	s := c.BeginParse()
	if s.MustLoadUInt(8) != 0x12 {
		panic("Unexpected validators_ext")
	}
	since = uint32(s.MustLoadUInt(32))
	until = uint32(s.MustLoadUInt(32))
	return
}
