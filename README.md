# go-pool-server

BitcoinCore(bitcoind)-variants' pool written in golang

## Difference from NOMP(node-node-open-mining-portal)

This pool software is not a portal, but a standalone stratum server with high performance.

If you want, you can implement the portal page in frontend web.

## Why standalone?

Keep standalone is better to assemble new algorithm/coin to your pool services, without dealing with C lib conflicts or restart the whole site to append new pool, what's more, most pool operators don't need a portal, they just get benefit from few different coins with different algorithms.

So it's obviously that standalone one with more advantages on deploying and maintaining.

In other words, "大人，時代變了！".

## How to use it?

### 0x00 Check

Make sure your algorithm in support, if not, take an issue. 

### 0x01 Configuration

Read `config.jsonc` and modify the configurations 

### 0x02 Build

```bash
go build .

```

### 0x03 Deploy

Copy `go-stratum-pool` or `go-stratum-pool.exe` and `config.json` to your VPS server and  

And then

```bash
$ ./go-stratum-pool

```

or

```cmd
> go-stratum-pool.exe

```

## TODO

- Main
- API
- More algorithms
- Web page
- ...

## Donation

**LTC**: LXxqHY4StG79nqRurdNNt1wF2yCf4Mc986
