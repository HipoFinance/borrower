package borrower

import (
	"context"
	"fmt"
	"math/big"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xssnick/tonutils-go/address"
)

type Tonlib struct {
	executable   string
	globalConfig string
}

func NewTonlib(executable string, globalConfig string) *Tonlib {
	return &Tonlib{
		executable:   executable,
		globalConfig: globalConfig,
	}
}

func (t *Tonlib) createCommand(command string) (out []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for i := 0; i < 3; i += 1 {
		out, err = exec.CommandContext(ctx, t.executable, "-c", t.globalConfig, "-E", command).Output()
		if err == nil {
			return
		}
	}
	return
}

func (t *Tonlib) GetParticipateSince(treasury address.Address) uint32 {
	out, err := t.createCommand(fmt.Sprintf("runmethod %s get_times", treasury.String()))
	if err != nil {
		panic(fmt.Sprintf("Error in tonlib runmethod for get_times: %v", err))
	}
	participateSinceString := getTonlibResult(out, 6, 1)
	participateSince, err := strconv.Atoi(participateSinceString)
	if err != nil {
		panic(fmt.Sprintf("Unexpected participate_since time: %s", out))
	}
	return uint32(participateSince)
}

func (t *Tonlib) GetMaxPunishment(treasury address.Address, stake *big.Int) *big.Int {
	out, err :=
		t.createCommand(fmt.Sprintf("runmethod %s get_max_punishment %d", treasury.String(), stake))
	if err != nil {
		panic(fmt.Sprintf("Error in tonlib runmethod for get_max_punishment: %v", err))
	}
	maxPunishmentString := getTonlibResult(out, 1, 0)
	maxPunishment, parsed := new(big.Int).SetString(maxPunishmentString, 10)
	if !parsed {
		panic(fmt.Sprintf("Unexpected max_punishment nanoTON string: %s", out))
	}
	return maxPunishment
}

func (t *Tonlib) GetRequestLoanFee(treasury address.Address) *big.Int {
	out, err := t.createCommand(fmt.Sprintf("runmethod %s get_treasury_fees 0", treasury.String()))
	if err != nil {
		panic(fmt.Sprintf("Error in tonlib runmethod for get_treasury_fees: %v", err))
	}
	requestLoanFeeString := getTonlibResult(out, 3, 0)
	requestLoanFee, parsed := new(big.Int).SetString(requestLoanFeeString, 10)
	if !parsed {
		panic(fmt.Sprintf("Unexpected request_loan_fee nanoTON string: %s", out))
	}
	return requestLoanFee
}

func getTonlibResult(out []byte, count int, index int) string {
	lines := strings.Split(strings.Trim(string(out), " \n\t"), "\n")
	if len(lines) < count+1 {
		panic(fmt.Sprintf("Unexpected tonlib out, expected at least %v lines, but got %v lines", count, len(lines)))
	}
	exitCode := lines[len(lines)-count-1]
	if !strings.HasPrefix(exitCode, "Got smc result. exit code: 0") {
		panic(fmt.Sprintf("Unexpected exit-code in tonlib: %s", out))
	}
	return strings.Trim(lines[len(lines)-count+index], " \n\t")
}
