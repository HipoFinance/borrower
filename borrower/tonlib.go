package borrower

import (
	"context"
	"fmt"
	"math/big"
	"os/exec"
	"strconv"
	"strings"

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

func (t *Tonlib) createCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, t.executable, "-c", t.globalConfig, "-E", command)
}

func (t *Tonlib) GetParticipateSince(ctx context.Context, treasury address.Address) (int, error) {
	out, err := t.createCommand(ctx, fmt.Sprintf("runmethod %s get_times", treasury.String())).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.Trim(string(out), "\n"), "\n")
	lines = lines[len(lines)-7:]
	if !strings.HasPrefix(lines[0], "Got smc result. exit code: 0,") {
		return 0, fmt.Errorf("exit code is not zero")
	}
	lines = lines[1:]
	participateSince, err := strconv.Atoi(lines[1])
	if err != nil {
		return 0, err
	}
	return participateSince, nil
}

func (t *Tonlib) GetRequestLoadFee(ctx context.Context, treasury address.Address) (*big.Int, error) {
	out, err := t.createCommand(ctx, fmt.Sprintf("runmethod %s get_fees", treasury.String())).Output()
	if err != nil {
		return big.NewInt(0), err
	}
	lines := strings.Split(strings.Trim(string(out), "\n"), "\n")
	lines = lines[len(lines)-8:]
	if !strings.HasPrefix(lines[0], "Got smc result. exit code: 0,") {
		return big.NewInt(0), fmt.Errorf("exit code is not zero")
	}
	lines = lines[1:]
	requestLoanFee, err := strconv.Atoi(lines[3])
	if err != nil {
		return big.NewInt(0), err
	}
	return big.NewInt(int64(requestLoanFee)), nil
}
