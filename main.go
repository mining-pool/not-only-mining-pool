package main

import (
	"encoding/json"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/poolManager"
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"muzzammil.xyz/jsonc"
	"os"
)

func main() {
	var conf config.Options
	if utils.FileExists("config.jsonc") {
		_, rawJson, err := jsonc.ReadFromFile("config.jsonc")
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(rawJson, &conf)
		if err != nil {
			log.Fatal(err)
		}
	} else if utils.FileExists("config.json") {
		log.Println("reading config from config.json")
		f, err := os.Open("config.json")
		if err != nil {
			log.Fatal(err)
		}

		err = json.NewDecoder(f).Decode(&conf)
		if err != nil {
			log.Fatal(err)
		}
	}

	p := poolManager.NewPool(&conf)
	p.Init()
	for {
		select {}
	}
}
