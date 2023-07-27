package borrower

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type Executor struct {
	config ValidatorEngine
}

func NewValidatorEngine(config ValidatorEngine) *Executor {
	return &Executor{
		config: config,
	}
}

func (e *Executor) createCommand(ctx context.Context, command string) *exec.Cmd {
	address := fmt.Sprintf("%v:%v", e.config.Ip, e.config.ControlPort)
	executable := e.config.Executable
	clientKey := e.config.ClientKey
	serverKey := e.config.ServerKey
	return exec.CommandContext(ctx, executable, "-k", clientKey, "-p", serverKey, "-a", address, "-c", command)
}

func (e *Executor) IsSync(ctx context.Context) bool {
	out, _ := e.createCommand(ctx, "getstats").Output()
	unixTime, err := strconv.Atoi(getLastTokenFromLine(string(out), "unixtime"))
	if err != nil {
		return false
	}
	masterchainBlockTime, err := strconv.Atoi(getLastTokenFromLine(string(out), "masterchainblocktime"))
	if err != nil {
		return false
	}
	return unixTime-masterchainBlockTime < 60
}

func (e *Executor) NewKey(ctx context.Context) string {
	out, _ := e.createCommand(ctx, "newkey").Output()
	keyHash := getLastTokenFromLine(string(out), "created new key")
	return keyHash
}

func (e *Executor) AddPermKey(ctx context.Context, keyHash string, roundSince uint32, expireAt uint32) string {
	out, _ := e.createCommand(ctx, fmt.Sprintf("addpermkey %s %d %d", keyHash, roundSince, expireAt)).Output()
	return strings.Trim(string(out), " \n\t")
}

func (e *Executor) AddTempKey(ctx context.Context, keyHash string, expireAt uint32) string {
	out, _ := e.createCommand(ctx, fmt.Sprintf("addtempkey %s %s %d", keyHash, keyHash, expireAt)).Output()
	return strings.Trim(string(out), " \n\t")
}

func (e *Executor) AddValidatorAddr(ctx context.Context, keyHash string, adnlAddress string, expireAt uint32) string {
	out, _ := e.createCommand(ctx, fmt.Sprintf("addvalidatoraddr %s %s %d", keyHash, keyHash, expireAt)).Output()
	return strings.Trim(string(out), " \n\t")
}

func (e *Executor) ExportPub(ctx context.Context, keyHash string) string {
	out, _ := e.createCommand(ctx, fmt.Sprintf("exportpub %s", keyHash)).Output()
	publicKey := getLastTokenFromLine(string(out), "got public key:")
	return publicKey
}

func (e *Executor) Sign(ctx context.Context, keyHash string, newStakeMsg *cell.Cell) string {
	message := newStakeMsg.Dump()
	out, _ := e.createCommand(ctx, fmt.Sprintf("sign %s %s", keyHash, message)).Output()
	signature := getLastTokenFromLine(string(out), "got signature")
	return signature
}

func getLastTokenFromLine(out string, prefix string) string {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if after, found := strings.CutPrefix(line, prefix); found {
			return strings.Trim(after, " \n\t")
		}
	}
	return ""
}
