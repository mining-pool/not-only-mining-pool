package jobManager

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"time"

	"github.com/mining-pool/not-only-mining-pool/algorithm"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemonManager"
	"github.com/mining-pool/not-only-mining-pool/merkletree"
	"github.com/mining-pool/not-only-mining-pool/transactions"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

type Job struct {
	GetBlockTemplate      *daemonManager.GetBlockTemplate
	Submits               []string
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
		log.Error(err)
	}
	prevHashReversed := hex.EncodeToString(utils.ReverseByteOrder(bPreviousBlockHash))

	transactionData := make([][]byte, len(rpcData.Transactions))
	for i := 0; i < len(rpcData.Transactions); i++ {
		transactionData[i], err = hex.DecodeString(rpcData.Transactions[i].Data)
		if err != nil {
			log.Error(err)
		}
	}

	txsBytes := GetTransactionBytes(rpcData.Transactions)
	merkleTree := merkletree.NewMerkleTree(txsBytes)
	merkleBranch := merkletree.GetMerkleHashes(merkleTree.Steps)
	generationTransaction := transactions.CreateGeneration(
		rpcData,
		poolAddressScript,
		extraNoncePlaceholder,
		reward,
		txMessages,
		recipients,
	)

	txData := make([][]byte, len(rpcData.Transactions))
	for i := 0; i < len(rpcData.Transactions); i++ {
		data, err := hex.DecodeString(rpcData.Transactions[i].Data)
		if err != nil {
			log.Panic("failed to decode tx: ", rpcData.Transactions[i])
		}

		txData[i] = data
	}

	log.Info("New Job, diff: ", bigDiff)

	return &Job{
		GetBlockTemplate:      rpcData,
		Submits:               nil,
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
		log.Warn("empty generation transaction", j.GenerationTransaction)
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
		log.Warn("transaction data is empty")
	}

	voteData := j.GetVoteData()
	if voteData == nil {
		log.Warn("no vote data")
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

	pos := 0
	copy(header[pos:], nonce) //4
	pos += len(nonce)
	copy(header[pos:], bits) //4
	pos += len(bits)
	copy(header[pos:], nTime) //4
	pos += len(nTime)
	copy(header[pos:], merkleRoot) //32
	pos += len(merkleRoot)
	copy(header[pos:], prevHash) //32
	pos += len(prevHash)
	binary.BigEndian.PutUint32(header[pos:], uint32(j.GetBlockTemplate.Version)) //4
	pos += 4

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

func (j *Job) GetJobParams(forceUpdate bool) []interface{} {
	return []interface{}{
		j.JobId,
		j.PrevHashReversed,
		hex.EncodeToString(j.GenerationTransaction[0]),
		hex.EncodeToString(j.GenerationTransaction[1]),
		j.MerkleBranch,
		hex.EncodeToString(utils.PackInt32BE(j.GetBlockTemplate.Version)),
		j.GetBlockTemplate.Bits,
		hex.EncodeToString(utils.PackUint32BE(uint32(time.Now().Unix()))), // Updated: implement time rolling
		forceUpdate,
	}
}

func GetTransactionBytes(txs []*daemonManager.TxParams) [][]byte {
	txHashes := make([][]byte, len(txs))
	for i := 0; i < len(txs); i++ {
		if txs[i].TxId != "" {
			txHashes[i] = utils.Uint256BytesFromHash(txs[i].TxId)
			continue
		}

		if txs[i].Hash != "" {
			txHashes[i] = utils.Uint256BytesFromHash(txs[i].Hash)
			continue
		}

		log.Panic("no hash or txid in transactions params")
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
