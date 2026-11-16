package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/pool"
	"github.com/mining-pool/not-only-mining-pool/utils"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

var log = logging.Logger("main")

const defaultConfigFileName = "config.json"

var (
	configFileName    = flag.String("c", defaultConfigFileName, "configuration file for pool")
	logLevel          = flag.String("l", "info", "log level")
	hashAdminPassword = flag.Bool("hash-admin-password", false, "read an administrator password from the terminal and print its bcrypt hash")
)

func main() {
	flag.Parse()
	if *hashAdminPassword {
		if err := printAdminPasswordHash(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	lvl, err := logging.LevelFromString(*logLevel)
	if err != nil {
		panic(err)
	}
	logging.SetAllLoggers(lvl)

	var conf config.Options
	if !utils.FileExists(*configFileName) {
		log.Panic("the config file " + *configFileName + " does not exist")
	}

	f, err := os.Open(*configFileName)
	if err != nil {
		log.Panic(err)
	}

	err = json.NewDecoder(f).Decode(&conf)
	if err != nil {
		log.Panic(err)
	}

	var p *pool.Pool
	if conf.Engine != "" && conf.Engine != "gbt" {
		// pluggable engines register themselves via build-tagged imports
		// (see engines_*.go); e.g. `go build -tags ethash` for ETC.
		p = pool.NewEnginePool(&conf)
	} else {
		p = pool.NewPool(&conf)
	}
	p.Init()
	for {
		select {}
	}
}

func printAdminPasswordHash() error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("use an interactive terminal to enter the administrator password")
	}
	fmt.Fprint(os.Stderr, "Administrator password (12–72 bytes): ")
	password, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	defer clear(password)
	if len(password) < 12 || len(password) > 72 {
		return fmt.Errorf("password must contain 12–72 bytes")
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	confirmation, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	defer clear(confirmation)
	if string(password) != string(confirmation) {
		return fmt.Errorf("passwords do not match")
	}
	hash, err := bcrypt.GenerateFromPassword(password, 12)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(hash))
	return err
}
