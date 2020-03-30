package p2pManager

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/utils"
	"io"
	"log"
	"net"
	"time"
)

type Peer struct {
	Magic []byte

	Verack                bool
	ValidConnectionConfig bool

	NetworkServices   []byte
	EmptyNetAddress   []byte
	UserAgent         []byte
	BlockStartHeight  []byte
	RelayTransactions []byte

	InvCodes        map[string]uint32
	Commands        map[string][]byte
	Options         *config.P2POptions
	Conn            net.Conn
	ProtocolVersion int

	BlockNotifyCh chan string
}

func NewPeer(protocolVersion int, options *config.P2POptions) *Peer {
	magic, err := hex.DecodeString(options.Magic)
	if err != nil {
		log.Fatal("magic hex string is incorrect")
	}

	networkServices, _ := hex.DecodeString("0100000000000000") //NODE_NETWORK services (value 1 packed as uint64)
	emptyNetAddress, _ := hex.DecodeString("010000000000000000000000000000000000ffff000000000000")
	userAgent := utils.VarStringBytes("/node-stratum/")
	blockStartHeight, _ := hex.DecodeString("00000000") // block start_height, can be empty

	//If protocol version is new enough, add do not relay transactions flag byte, outlined in BIP37
	//https://github.com/bitcoin/bips/blob/master/bip-0037.mediawiki#extensions-to-existing-messages
	var relayTransactions []byte
	if options.DisableTransactions {
		relayTransactions = []byte{0}
	} else {
		relayTransactions = []byte{}
	}

	return &Peer{
		Magic:                 magic,
		Verack:                false,
		ValidConnectionConfig: true,

		Options:         options,
		ProtocolVersion: protocolVersion,

		NetworkServices:   networkServices,
		EmptyNetAddress:   emptyNetAddress,
		UserAgent:         userAgent,
		BlockStartHeight:  blockStartHeight,
		RelayTransactions: relayTransactions,

		InvCodes: map[string]uint32{
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

		BlockNotifyCh: make(chan string),
	}
}

func (p *Peer) Init() {
	p.Connect()
}

func (p *Peer) Connect() {
	var err error
	p.Conn, err = net.Dial("tcp", p.Options.Addr())
	if err != nil {
		log.Fatal("failed to connect to coin's p2p port: ", err)
	}

	p.SetupMessageParser()
}

func (p *Peer) SetupMessageParser() {
	go func() {
		p.SendVersion()

		header := make([]byte, 24)
		for {
			n, err := p.Conn.Read(header)
			if err == io.EOF {
				continue
			}

			if err != nil || n < 24 {
				log.Println(err)
				continue
			}

			if !bytes.Equal(header[0:4], p.Magic) {
				continue
			}

			payload := make([]byte, binary.LittleEndian.Uint32(header[16:20]))
			_, err = p.Conn.Read(payload)
			if err != nil {
				log.Println(err)
				continue
			}

			if bytes.Equal(utils.Sha256d(payload)[0:4], header[20:24]) {
				go p.HandleMessage(header[4:16], payload)
			}
		}
	}()

}

func (p *Peer) HandleMessage(command, payload []byte) {
	log.Println("handling: ", command, payload)
	switch string(command) {
	case string(p.Commands["inv"]):
		p.HandleInv(payload)
	case string(p.Commands["verack"]):
		if !p.Verack {
			p.Verack = true
			// connected
		}
	case string(p.Commands["version"]):
		p.SendMessage(p.Commands["verack"], make([]byte, 0))
	default:
		break
	}
}

//Parsing inv message https://en.bitcoin.it/wiki/Protocol_specification#inv
func (p *Peer) HandleInv(payload []byte) {
	//sloppy varint decoding
	var count int
	var buf []byte
	if payload[0] < 0xFD {
		count = int(payload[0])
		buf = payload[1:]
	} else {
		count = int(binary.LittleEndian.Uint16(payload[0:2]))
		buf = payload[2:]
	}

	for count--; count != 0; count-- {
		switch binary.LittleEndian.Uint32(buf) {
		case p.InvCodes["error"]:
		case p.InvCodes["tx"]:
			//tx := hex.EncodeToString(buf[4:36])
		case p.InvCodes["block"]:
			block := hex.EncodeToString(buf[4:36])
			log.Println("block found: ", block)
			// block found
			p.ProcessBlockNotify(block)
		}
		buf = buf[36:]
	}
}

func (p *Peer) SendMessage(command, payload []byte) {
	log.Println("sending: ", command, payload)
	if p.Conn == nil {
		p.Connect()
	}

	message := bytes.Join([][]byte{
		p.Magic,
		command,
		utils.PackUint32LE(uint32(len(payload))),
		utils.Sha256d(payload)[0:4],
		payload,
	}, nil)

	_, err := p.Conn.Write(message)
	if err != nil {
		log.Println(err)
	}

	log.Println(string(message))
}

func (p *Peer) SendVersion() {
	nonce := make([]byte, 8)
	rand.Read(nonce)
	payload := bytes.Join([][]byte{
		utils.PackUint32LE(uint32(p.ProtocolVersion)),
		p.NetworkServices,
		utils.PackUint64LE(uint64(time.Now().Unix())),
		p.EmptyNetAddress, //addr_recv, can be empty

		p.EmptyNetAddress, //addr_from, can be empty
		nonce,             //nonce, random unique ID
		p.UserAgent,
		p.BlockStartHeight,

		p.RelayTransactions,
	}, nil)

	p.SendMessage(p.Commands["version"], payload)
}

func (p *Peer) ProcessBlockNotify(blockHash string) {
	log.Println("Block notification via p2p")
	//if p.JobManager.CurrentJob != nil && blockHash != p.JobManager.CurrentJob.GetBlockTemplate.PreviousBlockHash {
	p.BlockNotifyCh <- blockHash
	//}
}
