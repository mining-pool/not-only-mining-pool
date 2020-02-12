package main

import (
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/poolManager"
	"log"
	"os"
)

func main() {
	var conf config.Options
	f, _ := os.Open("config.json")

	_ = json.NewDecoder(f).Decode(&conf)

	p := poolManager.NewPool(&conf)
	p.Init()
	p.SetupRecipients()
	p.SetupBlockPolling()
	p.StartStratumServer()
	if !p.CheckAllSynced() {
		log.Fatal("Not synced!")
	}

	//p.ProcessBlockNotify()
	p.OutputPoolInfo()
	for {
		select {}
	}
}
