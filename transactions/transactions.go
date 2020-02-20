package transactions

import (
	"bytes"
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"math"
	"time"
)

//type  struct {
//	CoinbaseValue            uint64 // unit: Satoshis
//	MasterNode               *MasternodeParams
//	Superblock               []SuperBlockTemplate
//	DefaultWitnessCommitment string
//}

func GenerateOutputTransactions(poolRecipient []byte, recipients map[string]float64, rpcData *daemonManager.GetBlockTemplate) []byte {
	reward := rpcData.CoinbaseValue
	rewardToPool := reward
	txOutputBuffers := make([][]byte, 0)

	if rpcData.Masternode != nil && rpcData.Superblock != nil {
		if len(rpcData.Masternode) > 0 {
			for i := range rpcData.Masternode {
				payeeReward := rpcData.Masternode[i].Amount
				reward -= payeeReward
				rewardToPool -= payeeReward

				payeeScript := utils.P2PKHAddressToScript(rpcData.Masternode[i].Payee)
				txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
					utils.PackUint64BE(payeeReward),
					utils.VarIntBytes(uint64(len(payeeScript))),
				}, nil))
			}
		} else if len(rpcData.Superblock) > 0 {
			for i := range rpcData.Superblock {
				payeeReward := rpcData.Superblock[i].Amount
				reward -= payeeReward
				rewardToPool -= payeeReward

				payeeScript := utils.P2PKHAddressToScript(rpcData.Superblock[i].Payee)
				txOutputBuffers = append(txOutputBuffers, bytes.Join([][]byte{
					utils.PackUint64LE(payeeReward),
					utils.VarIntBytes(uint64(len(payeeScript))),
					payeeScript,
				}, nil))
			}
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
		script := utils.P2SHAddressToScript(i)

		recipientReward := uint64(math.Floor(recipients[i] * float64(reward)))
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
		log.Println("having DefaultWitnessCommitment", rpcData.DefaultWitnessCommitment)
		witnessCommitment, err := hex.DecodeString(rpcData.DefaultWitnessCommitment)
		if err != nil {
			log.Println(err)
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

func CreateGeneration(rpcData *daemonManager.GetBlockTemplate, publicKey, extraNoncePlaceholder []byte, reward string, txMessages bool, recipients map[string]float64) [][]byte {
	var txVersion int
	var txComment []byte
	if txMessages {
		txVersion = 2
		txComment = utils.SerializeString("by Command")
	} else {
		txVersion = 1
		txComment = make([]byte, 0)
	}
	txLockTime := 0

	txInPrevOutHash := ""
	txInPrevOutIndex := 1<<32 - 1
	txInSequence := 0

	txTimestamp := make([]byte, 0)
	if reward == "POS" {
		txTimestamp = utils.PackUint32LE(uint32(rpcData.CurTime))
	}

	bCoinbaseAuxFlags, err := hex.DecodeString(rpcData.CoinbaseAux.Flags)
	if err != nil {
		log.Println(err)
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

	return [][]byte{p1, p2}
}
