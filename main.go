package main

import (
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/poolManager"
	"log"
	"muzzammil.xyz/jsonc"
	"os"
)

func main() {
	var conf config.Options

	if _, err := os.Stat("config.jsonc"); os.IsExist(err) {
		_, rawJson, err := jsonc.ReadFromFile("config.jsonc")
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(rawJson, &conf)
		if err != nil {
			log.Fatal(err)
		}
	}

	if _, err := os.Stat("config.json"); os.IsExist(err) {
		_, rawJson, err := jsonc.ReadFromFile("config.json")
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(rawJson, &conf)
		if err != nil {
			log.Fatal(err)
		}
	}

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
