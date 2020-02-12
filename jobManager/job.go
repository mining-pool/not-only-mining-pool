package jobManager

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/algorithm"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/merkletree"
	"github.com/node-standalone-pool/go-pool-server/transactions"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"math/big"
)

type Job struct {
	GetBlockTemplate      *daemonManager.GetBlockTemplate
	Submits               []string
	JobParams             []interface{}
	generationTransaction [][]byte
	JobId                 string
	PrevHashReversed      string
	MerkleBranch          []string
	Target                *big.Int
	Difficulty            *big.Float
	TransactionData       []byte
	Reward                string
	MerkleTree            *merkletree.MerkleTree
}

func NewJob(jobId string, rpcData *daemonManager.GetBlockTemplate, poolAddressScript, extraNoncePlaceholder []byte, reward string, txMessages bool, recipients map[string]float64) *Job {
	var bigTarget *big.Int

	if rpcData.Target != "" {
		bigTarget, _ = new(big.Int).SetString(rpcData.Target, 16)
	} else {
		utils.BigIntFromBitsHex(rpcData.Bits)
	}

	bigDiff := new(big.Float).Quo(
		new(big.Float).SetInt(algorithm.MaxTargetTruncated),
		new(big.Float).SetInt(bigTarget),
	)

	bPreviousBlockHash, err := hex.DecodeString(rpcData.PreviousBlockHash)
	if err != nil {
		log.Println(err)
	}
	prevHashReversed := hex.EncodeToString(utils.ReverseByteOrder(bPreviousBlockHash))

	transactionData := make([][]byte, len(rpcData.Transactions))
	for i := 0; i < len(rpcData.Transactions); i++ {
		transactionData[i], err = hex.DecodeString(rpcData.Transactions[i].Data)
		if err != nil {
			log.Println(err)
		}
	}

	merkleTree := merkletree.NewMerkleTree(GetTransactionBuffers(rpcData.Transactions))
	merkleBranch := merkletree.GetMerkleHashes(merkleTree.Steps)
	generationTransaction := transactions.CreateGeneration(
		rpcData,
		poolAddressScript,
		extraNoncePlaceholder,
		reward,
		txMessages,
		recipients,
	)

	log.Println("New Job, diff:", bigDiff)

	return &Job{
		GetBlockTemplate:      rpcData,
		Submits:               nil,
		JobParams:             nil,
		generationTransaction: generationTransaction,
		JobId:                 jobId,
		PrevHashReversed:      prevHashReversed,
		MerkleTree:            merkleTree,
		MerkleBranch:          merkleBranch,
		Target:                bigTarget,
		Difficulty:            bigDiff,
	}
}

func (j *Job) SerializeCoinbase(extraNonce1, extraNonce2 []byte) []byte {
	if j.generationTransaction[0] == nil || j.generationTransaction[1] == nil {
		log.Println("warning: empty generation transaction", j.generationTransaction)
	}

	return bytes.Join([][]byte{
		j.generationTransaction[0],
		extraNonce1,
		extraNonce2,
		j.generationTransaction[1],
	}, nil)
}

func (j *Job) SerializeBlock(header, coinbase []byte) []byte {
	//POS coins require a zero byte appended to block which the daemon replaces with the signature
	var suffix []byte
	if j.Reward == "POS" {
		suffix = []byte{0}
	}

	return bytes.Join([][]byte{
		header,
		utils.VarIntBytes(uint64(len(j.GetBlockTemplate.Transactions) + 1)),
		coinbase,
		j.TransactionData,
		j.GetVoteData(),
		suffix,
	}, nil)
}

//https://en.bitcoin.it/wiki/Protocol_specification#Block_Headers
func (j *Job) SerializeHeader(merkleRoot, nTime, nonce []byte) []byte {
	header := make([]byte, 80)

	bits, _ := hex.DecodeString(j.GetBlockTemplate.Bits)
	prevHash, _ := hex.DecodeString(j.GetBlockTemplate.PreviousBlockHash)

	copy(header[0:], nonce)                                                     //4
	copy(header[4:], bits)                                                      //4
	copy(header[8:], nTime)                                                     //4
	copy(header[12:], merkleRoot)                                               //32
	copy(header[44:], prevHash)                                                 //32
	binary.BigEndian.PutUint32(header[76:], uint32(j.GetBlockTemplate.Version)) //4

	return utils.ReverseBytes(header)
}

// record the submit times and contents => check duplicate
func (j *Job) RegisterSubmit(extraNonce1, extraNonce2, nTime, nonce string) bool {
	submission := extraNonce1 + extraNonce2 + nTime + nonce

	if utils.StringsIndexOf(j.Submits, submission) == -1 {
		j.Submits = append(j.Submits, submission)
		return true
	}

	return false
}

func (j *Job) GetJobParams() []interface{} {
	if j.JobParams == nil {
		generationTransaction0 := hex.EncodeToString(j.generationTransaction[0])
		generationTransaction1 := hex.EncodeToString(j.generationTransaction[1])

		j.JobParams = []interface{}{
			j.JobId,
			j.PrevHashReversed,
			generationTransaction0,
			generationTransaction1,
			j.MerkleBranch,
			hex.EncodeToString(utils.PackUint32BE(uint32(j.GetBlockTemplate.Version))),
			j.GetBlockTemplate.Bits,
			hex.EncodeToString(utils.PackUint32BE(uint32(j.GetBlockTemplate.CurTime))),
			true,
		}
	}

	return j.JobParams
}

func GetTransactionBuffers(txs []*daemonManager.TxParams) [][]byte {
	var txHashes [][]byte
	for i := 0; i < len(txs); i++ {
		if txs[i].TxId != "" {
			txHashes = append(txHashes, utils.Uint256BytesFromHash(txs[i].TxId))
		}
		txHashes = append(txHashes, utils.Uint256BytesFromHash(txs[i].Hash))
	}

	return append([][]byte{nil}, txHashes...)
}

func (j *Job) GetVoteData() []byte {
	if j.GetBlockTemplate.MasternodePayments == nil {
		return nil
	}

	hexVotes := make([][]byte, len(j.GetBlockTemplate.Votes))
	for i := 0; i < len(j.GetBlockTemplate.Votes); i++ {
		hexVotes[i], _ = hex.DecodeString(j.GetBlockTemplate.Votes[i])
	}

	return bytes.Join([][]byte{
		utils.VarIntBytes(uint64(len(j.GetBlockTemplate.Votes))),
	}, nil)
}
