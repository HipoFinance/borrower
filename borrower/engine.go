package borrower

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

type Engine struct {
	config ValidatorEngine
}

func NewValidatorEngine(config ValidatorEngine) *Engine {
	return &Engine{
		config: config,
	}
}

func (e *Engine) createCommand(command string) ([]byte, error) {
	address := fmt.Sprintf("%v:%v", e.config.Ip, e.config.ControlPort)
	executable := e.config.Executable
	clientKey := e.config.ClientKey
	serverKey := e.config.ServerKey
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, executable, "-k", clientKey, "-p", serverKey, "-a", address, "-c", command).Output()
}

func (e *Engine) IsSync() bool {
	out, err := e.createCommand("getstats")
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console getstats: %v", err))
	}
	unixTime, err := strconv.Atoi(getLastTokenFromLine(out, "unixtime"))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console getstats: %s", out))
	}
	masterchainBlockTime, err := strconv.Atoi(getLastTokenFromLine(out, "masterchainblocktime"))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console getstats: %s", out))
	}
	return unixTime-masterchainBlockTime < 60
}

func (e *Engine) NewKey() string {
	out, err := e.createCommand("newkey")
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console newkey: %v", err))
	}
	keyHash := getLastTokenFromLine(out, "created new key")
	if keyHash == "" {
		panic(fmt.Sprintf("Error in validator-console newkey: %v", string(out)))
	}
	return keyHash
}

func (e *Engine) AddPermKey(keyHash string, roundSince uint32, expireAt uint32) {
	out, err := e.createCommand(fmt.Sprintf("addpermkey %s %d %d", keyHash, roundSince, expireAt))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console addpermkey: %v", err))
	}
	if getStatus(out) != "success" {
		panic(fmt.Sprintf("Error in validator-console addpermkey: %s", out))
	}
}

func (e *Engine) AddTempKey(keyHash string, expireAt uint32) {
	out, err := e.createCommand(fmt.Sprintf("addtempkey %s %s %d", keyHash, keyHash, expireAt))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console addtempkey: %v", err))
	}
	if getStatus(out) != "success" {
		panic(fmt.Sprintf("Error in validator-console addtempkey: %s", out))
	}
}

func (e *Engine) AddValidatorAddr(keyHash string, adnlAddress string, expireAt uint32) {
	out, err := e.createCommand(fmt.Sprintf("addvalidatoraddr %s %s %d", keyHash, adnlAddress, expireAt))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console addvalidatoraddr: %v", err))
	}
	if getStatus(out) != "success" {
		panic(fmt.Sprintf("Error in validator-console addvalidatoraddr %s", out))
	}
}

func (e *Engine) ExportPub(keyHash string) []byte {
	out, err := e.createCommand(fmt.Sprintf("exportpub %s", keyHash))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console exportpub: %v", err))
	}
	publicKeyBase64 := getLastTokenFromLine(out, "got public key:")
	if publicKeyBase64 == "" {
		panic(fmt.Sprintf("Error in validator-console exportpub: %s", out))
	}
	publicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		panic(fmt.Sprintf("Error in base64 decoding of publickey: %v", err))
	}
	if len(publicKey) > 32 {
		publicKey = publicKey[len(publicKey)-32:]
	}
	return publicKey
}

func (e *Engine) Sign(keyHash string, newStakeMsg *cell.Cell) []byte {
	message := newStakeMsg.Dump()
	message = strings.Split(message, "[")[1]
	message = strings.Split(message, "]")[0]
	out, err := e.createCommand(fmt.Sprintf("sign %s %s", keyHash, message))
	if err != nil {
		panic(fmt.Sprintf("Error in validator-console sign: %v", err))
	}
	signatureBase64 := getLastTokenFromLine(out, "got signature")
	signatureBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		panic(fmt.Sprintf("Error in base64 decoding of signature: %v", err))
	}
	return signatureBytes
}

func getLastTokenFromLine(out []byte, prefix string) string {
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if after, found := strings.CutPrefix(line, prefix); found {
			return strings.Trim(after, " \n\t")
		}
	}
	return ""
}

func getStatus(out []byte) string {
	lines := strings.Split(strings.Trim(string(out), " \n\t"), "\n")
	return strings.Trim(lines[len(lines)-1], " \n\t")
}
