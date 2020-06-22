package transactions

import (
	"bytes"
	"encoding/hex"
	logging "github.com/ipfs/go-log"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemonManager"
	"github.com/mining-pool/not-only-mining-pool/utils"
	"math"
	"time"
)

var log = logging.Logger("tx")

//type  struct {
//	CoinbaseValue            uint64 // unit: Satoshis
//	MasterNode               *MasternodeParams
//	Superblock               []SuperBlockTemplate
//	DefaultWitnessCommitment string
//}

func GenerateOutputTransactions(poolRecipient []byte, recipients []*config.Recipient, rpcData *daemonManager.GetBlockTemplate) []byte {
	reward := rpcData.CoinbaseValue
	rewardToPool := reward
	txOutputBuffers := make([][]byte, 0)

	if rpcData.Masternode != nil && len(rpcData.Masternode) > 0 {
		log.Info("handling dash's masternode")
		for i := range rpcData.Masternode {
			payeeReward := rpcData.Masternode[i].Amount
			reward -= payeeReward
			rewardToPool -= payeeReward

			var payeeScript []byte
			if len(rpcData.Masternode[i].Script) > 0 {
				payeeScript, _ = hex.DecodeString(rpcData.Masternode[i].Script)
			} else {
				payeeScript = utils.P2PKHAddressToScript(rpcData.Masternode[i].Payee)
			}
			txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
				utils.PackUint64BE(payeeReward),
				utils.VarIntBytes(uint64(len(payeeScript))),
			}, nil))
		}
	}

	if rpcData.Superblock != nil && len(rpcData.Superblock) > 0 {
		log.Info("handling dash's superblock")
		for i := range rpcData.Superblock {
			payeeReward := rpcData.Superblock[i].Amount
			reward -= payeeReward
			rewardToPool -= payeeReward

			var payeeScript []byte
			if len(rpcData.Superblock[i].Script) > 0 {
				payeeScript, _ = hex.DecodeString(rpcData.Superblock[i].Script)
			} else {
				payeeScript = utils.P2PKHAddressToScript(rpcData.Superblock[i].Payee)
			}

			txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
				utils.PackUint64LE(payeeReward),
				utils.VarIntBytes(uint64(len(payeeScript))),
				payeeScript,
			}, nil))
		}
	}

	if rpcData.Payee != nil {
		var payeeReward uint64
		if rpcData.PayeeAmount != nil {
			payeeReward = rpcData.PayeeAmount.(uint64)
		} else {
			payeeReward = uint64(math.Ceil(float64(reward) / 5))
		}

		reward -= payeeReward
		rewardToPool -= payeeReward

		payeeScript := utils.P2PKHAddressToScript(rpcData.Payee.(string))
		txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
			utils.PackUint64LE(payeeReward),
			utils.VarIntBytes(uint64(len(payeeScript))),
			payeeScript,
		}, nil))
	}

	for i := range recipients {
		script := recipients[i].GetScript()

		recipientReward := uint64(math.Floor(recipients[i].Percent * float64(reward)))
		rewardToPool -= recipientReward

		txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
			utils.PackUint64LE(recipientReward),
			utils.VarIntBytes(uint64(len(script))),
			script,
		}, nil))
	}

	txOutputBuffers = append([][]byte{bytes.Join([][]byte{
		utils.PackUint64LE(rewardToPool),
		utils.VarIntBytes(uint64(len(poolRecipient))),
		poolRecipient,
	}, nil)}, txOutputBuffers...)

	if rpcData.DefaultWitnessCommitment != "" {
		witnessCommitment, err := hex.DecodeString(rpcData.DefaultWitnessCommitment)
		if err != nil {
			log.Error(err)
		}

		txOutputBuffers = append([][]byte{bytes.Join([][]byte{
			utils.PackUint64LE(0),
			utils.VarIntBytes(uint64(len(witnessCommitment))),
			witnessCommitment,
		}, nil)}, txOutputBuffers...)
	}

	return bytes.Join([][]byte{
		utils.VarIntBytes(uint64(len(txOutputBuffers))),
		bytes.Join(txOutputBuffers, nil),
	}, nil)
}

func CreateGeneration(rpcData *daemonManager.GetBlockTemplate, publicKey, extraNoncePlaceholder []byte, reward string, txMessages bool, recipients []*config.Recipient) [][]byte {
	var txVersion int
	var txComment []byte
	var txType = 0
	var txExtraPayload []byte
	if txMessages {
		txVersion = 2
		txComment = utils.SerializeString("by Command")
	} else {
		txVersion = 1
		txComment = make([]byte, 0)
	}
	txLockTime := 0

	if rpcData.CoinbasePayload != "" && len(rpcData.CoinbasePayload) > 0 {
		txVersion = 3
		txType = 5
		txExtraPayload, _ = hex.DecodeString(rpcData.CoinbasePayload)
	}

	txVersion = txVersion + (txType << 16)

	txInPrevOutHash := ""
	txInPrevOutIndex := 1<<32 - 1
	txInSequence := 0

	txTimestamp := make([]byte, 0)
	if reward == "POS" {
		txTimestamp = utils.PackUint32LE(rpcData.CurTime)
	}

	bCoinbaseAuxFlags, err := hex.DecodeString(rpcData.CoinbaseAux.Flags)
	if err != nil {
		log.Error(err)
	}
	scriptSigPart1 := bytes.Join([][]byte{
		utils.SerializeNumber(uint64(rpcData.Height)),
		bCoinbaseAuxFlags,
		utils.SerializeNumber(uint64(time.Now().Unix())),
		{byte(len(extraNoncePlaceholder))},
	}, nil)

	scriptSigPart2 := utils.SerializeString("/by Command/")

	p1 := bytes.Join([][]byte{
		utils.PackUint32LE(uint32(txVersion)),
		txTimestamp,

		//transaction input
		utils.VarIntBytes(1), // only one txIn
		utils.Uint256BytesFromHash(txInPrevOutHash),
		utils.PackUint32LE(uint32(txInPrevOutIndex)),
		utils.VarIntBytes(uint64(len(scriptSigPart1) + len(extraNoncePlaceholder) + len(scriptSigPart2))),
		scriptSigPart1,
	}, nil)

	outputTransactions := GenerateOutputTransactions(publicKey, recipients, rpcData)

	p2 := bytes.Join([][]byte{
		scriptSigPart2,
		utils.PackUint32LE(uint32(txInSequence)),
		//end transaction input

		//transaction output
		outputTransactions,
		//end transaction ouput

		utils.PackUint32LE(uint32(txLockTime)),
		txComment,
	}, nil)

	if len(txExtraPayload) > 0 {
		p2 = bytes.Join([][]byte{
			p2,
			utils.VarIntBytes(uint64(len(txExtraPayload))),
			txExtraPayload,
		}, nil)
	}

	return [][]byte{p1, p2}
}
