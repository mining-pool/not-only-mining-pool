package main

import (
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/poolManager"
	"log"
	"muzzammil.xyz/jsonc"
)

func main() {
	var conf config.Options

	_, rawJson, _ := jsonc.ReadFromFile("config.jsonc")
	_ = json.Unmarshal(rawJson, &conf)

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
