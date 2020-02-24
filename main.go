package main

import (
	"encoding/json"
	"flag"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/poolManager"
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"os"
)

const defaultConfigFileName = "config.json"

var configFileName = flag.String("c", defaultConfigFileName, "configuration file for pool")

func main() {
	var conf config.Options
	if !utils.FileExists(*configFileName) {
		log.Fatal("the config file " + *configFileName + " does not exist")
	}

	f, err := os.Open(*configFileName)
	if err != nil {
		log.Fatal(err)
	}

	err = json.NewDecoder(f).Decode(&conf)
	if err != nil {
		log.Fatal(err)
	}

	p := poolManager.NewPool(&conf)
	p.Init()
	for {
		select {}
	}
}
