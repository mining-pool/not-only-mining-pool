package jobManager

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/algorithm"
	"github.com/node-standalone-pool/go-pool-server/config"
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
	GenerationTransaction [][]byte
	JobId                 string
	PrevHashReversed      string
	MerkleBranch          []string
	Target                *big.Int
	Difficulty            *big.Float
	TransactionData       []byte
	Reward                string
	MerkleTree            *merkletree.MerkleTree
}

func NewJob(jobId string, rpcData *daemonManager.GetBlockTemplate, poolAddressScript, extraNoncePlaceholder []byte, reward string, txMessages bool, recipients []*config.Recipient) *Job {
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

	merkleTree := merkletree.NewMerkleTree(GetTransactionBytes(rpcData.Transactions))
	merkleBranch := merkletree.GetMerkleHashes(merkleTree.Steps)
	generationTransaction := transactions.CreateGeneration(
		rpcData,
		poolAddressScript,
		extraNoncePlaceholder,
		reward,
		txMessages,
		recipients,
	)

	var txData [][]byte
	for i := 0; i < len(rpcData.Transactions); i++ {
		data, err := hex.DecodeString(rpcData.Transactions[i].Data)
		if err != nil {
			log.Fatal("failed to decode tx:", rpcData.Transactions[i])
		}

		txData = append(txData, data)
	}

	log.Println("New Job, diff:", bigDiff)

	return &Job{
		GetBlockTemplate:      rpcData,
		Submits:               nil,
		JobParams:             nil,
		GenerationTransaction: generationTransaction,
		JobId:                 jobId,
		PrevHashReversed:      prevHashReversed,
		MerkleBranch:          merkleBranch,
		Target:                bigTarget,
		Difficulty:            bigDiff,
		TransactionData:       bytes.Join(txData, nil),
		Reward:                "",
		MerkleTree:            merkleTree,
	}
}

func (j *Job) SerializeCoinbase(extraNonce1, extraNonce2 []byte) []byte {
	if j.GenerationTransaction[0] == nil || j.GenerationTransaction[1] == nil {
		log.Println("warning: empty generation transaction", j.GenerationTransaction)
	}

	return bytes.Join([][]byte{
		j.GenerationTransaction[0],
		extraNonce1,
		extraNonce2,
		j.GenerationTransaction[1],
	}, nil)
}

func (j *Job) SerializeBlock(header, coinbase []byte) []byte {
	//POS coins require a zero byte appended to block which the daemon replaces with the signature
	var suffix []byte
	if j.Reward == "POS" {
		suffix = []byte{0}
	} else {
		suffix = []byte{}
	}

	if j.TransactionData == nil {
		log.Println("warning: TransactionData is empty")
	}

	voteData := j.GetVoteData()
	if voteData == nil {
		log.Println("no vote data")
	}

	return bytes.Join([][]byte{
		header,

		utils.VarIntBytes(uint64(len(j.GetBlockTemplate.Transactions) + 1)), // coinbase(generation) + txs
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
		j.JobParams = []interface{}{
			j.JobId,
			j.PrevHashReversed,
			hex.EncodeToString(j.GenerationTransaction[0]),
			hex.EncodeToString(j.GenerationTransaction[1]),
			j.MerkleBranch,
			hex.EncodeToString(utils.PackInt32BE(j.GetBlockTemplate.Version)),
			j.GetBlockTemplate.Bits,
			hex.EncodeToString(utils.PackUint32BE(j.GetBlockTemplate.CurTime)),
			true,
		}
	}

	return j.JobParams
}

func GetTransactionBytes(txs []*daemonManager.TxParams) [][]byte {
	var txHashes [][]byte
	for i := 0; i < len(txs); i++ {
		if txs[i].TxId != "" {
			txHashes = append(txHashes, utils.Uint256BytesFromHash(txs[i].TxId))
		} else {
			txHashes = append(txHashes, utils.Uint256BytesFromHash(txs[i].Hash))
		}
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
