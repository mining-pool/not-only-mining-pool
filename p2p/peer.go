package p2p

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"net"
)

type Peer struct {
	Magic    []byte
	MagicInt uint32

	Verack                bool
	ValidConnectionConfig bool

	NetworkServices   []byte
	EmptyNetAddress   []byte
	UserAgent         []byte
	BlockStartHeight  []byte
	RelayTransactions []byte

	InvCodes map[string]int
	Commands map[string][]byte
	Options  config.Options
}

func NewPeer(options config.Options) *Peer {
	if options.P2P == nil || options.P2P.Enabled == false {
		return nil
	}

	magic, err := hex.DecodeString(options.Coin.PeerMagic)
	if err != nil {
		log.Fatal("magic hex string is incorrect")
	}
	magicInt := binary.LittleEndian.Uint32(magic)

	networkServices, _ := hex.DecodeString("0100000000000000") //NODE_NETWORK services (value 1 packed as uint64)
	emptyNetAddress, _ := hex.DecodeString("010000000000000000000000000000000000ffff000000000000")
	userAgent := utils.VarStringBytes("/node-stratum/")
	blockStartHeight, _ := hex.DecodeString("00000000") // block start_height, can be empty

	//If protocol version is new enough, add do not relay transactions flag byte, outlined in BIP37
	//https://github.com/bitcoin/bips/blob/master/bip-0037.mediawiki#extensions-to-existing-messages
	var relayTransactions []byte
	if options.P2P.DisableTransactions {
		relayTransactions = []byte{0}
	} else {
		relayTransactions = []byte{}
	}

	return &Peer{
		Magic:                 magic,
		MagicInt:              magicInt,
		Verack:                false,
		ValidConnectionConfig: true,

		Options: options,

		NetworkServices:   networkServices,
		EmptyNetAddress:   emptyNetAddress,
		UserAgent:         userAgent,
		BlockStartHeight:  blockStartHeight,
		RelayTransactions: relayTransactions,

		InvCodes: map[string]int{
			"error": 0,
			"tx":    1,
			"block": 2,
		},

		Commands: map[string][]byte{
			"version":   utils.CommandStringBytes("version"),
			"inv":       utils.CommandStringBytes("inv"),
			"verack":    utils.CommandStringBytes("verack"),
			"addr":      utils.CommandStringBytes("addr"),
			"getblocks": utils.CommandStringBytes("getblocks"),
		},
	}
}

func (p *Peer) Connect() {
	conn, err := net.Dial("tcp", p.Options.P2P.Addr())
	if err != nil {
		log.Fatal("failed to connect to coin's p2p port: ", err)
	}

	go func() {
		r := bufio.NewReader(conn)
		for {
			r.ReadBytes('\n')
		}
	}()
}

func (p *Peer) SetupMessageParser() {

}

func (p *Peer) SendMessage() {

}

func (p *Peer) SendVersion() {

}

func (p *Peer) HandleMessage() {

}

func (p *Peer) HandleInv() {

}

//func ReadFlowingBytes(stream, amount int, preRead []byte) {
//	var b []byte
//	if preRead != nil {
//		b = preRead
//	}else{
//		b = make([]byte, 0)
//	}
//
//	data := make([]byte, 0)
//	b = bytes.Join([][]byte{b, data}, nil)
//	if (len(b) >= amount) {
//		var returnData = b[0: amount]
//		var lopped []byte
//		if len(b) > amount {
//			lopped = b[amount:]
//		}
//
//		callback(returnData, lopped)
//	} else {
//		stream.once("data", readData) // TODO
//	}
//}
//
