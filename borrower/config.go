package borrower

import (
	"math/big"
	"os"

	"github.com/xssnick/tonutils-go/tvm/cell"
	"gopkg.in/yaml.v3"
)

var ConfigFile = "borrower.yaml"

type Config struct {
	Treasury        string
	Borrow          Borrow
	Wallet          Wallet
	ValidatorEngine ValidatorEngine `yaml:"validator_engine"`
	TonlibCli       TonlibCli       `yaml:"tonlib_cli"`
}

type Borrow struct {
	Active               bool
	Stake                string
	Loan                 string
	MinPayment           string  `yaml:"min_payment"`
	MaxFactorRatio       float32 `yaml:"max_factor_ratio"`
	ValidatorRewardShare uint8   `yaml:"validator_reward_share"`
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

type TonlibCli struct {
	Executable   string
	GlobalConfig string `yaml:"global_config"`
}

func ReadConfig() (config *Config, err error) {
	contents, err := os.ReadFile(ConfigFile)
	if err != nil {
		return
	}

	err = yaml.Unmarshal(contents, &config)
	return
}

var ConfigStake int32 = 17
var ConfigCurrentValidators int32 = 34

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
