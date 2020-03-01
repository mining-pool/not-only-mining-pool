package jobManager

import (
	"encoding/hex"
	"github.com/mining-pool/go-pool-server/algorithm"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/daemonManager"
	"github.com/mining-pool/go-pool-server/storageManager"
	"github.com/mining-pool/go-pool-server/types"
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"
)

type JobCounter struct {
	Counter *big.Int
}

type JobManager struct {
	Storage               *storageManager.Storage
	Options               *config.Options
	JobCounter            *JobCounter
	ExtraNonce1Generator  *ExtraNonce1Generator
	ExtraNoncePlaceholder []byte
	ExtraNonce2Size       int

	CurrentJob *Job
	ValidJobs  map[string]*Job

	CoinbaseHasher  func([]byte) []byte
	ValidateAddress *daemonManager.ValidateAddress

	DaemonManager *daemonManager.DaemonManager

	NewBlockEvent chan *Job
}

func NewJobManager(options *config.Options, validateAddress *daemonManager.ValidateAddress, dm *daemonManager.DaemonManager) *JobManager {
	placeholder, _ := hex.DecodeString("f000000ff111111f")
	extraNonce1Generator := NewExtraNonce1Generator()

	return &JobManager{
		Options:               options,
		ExtraNonce1Generator:  extraNonce1Generator,
		ExtraNoncePlaceholder: placeholder,
		ExtraNonce2Size:       len(placeholder) - extraNonce1Generator.Size,
		CurrentJob:            nil,
		ValidJobs:             make(map[string]*Job),
		CoinbaseHasher:        utils.Sha256d,
		ValidateAddress:       validateAddress,

		DaemonManager: dm,
	}
}

func (jm *JobManager) Init(gbt *daemonManager.GetBlockTemplate) {
	jm.ProcessTemplate(gbt)
}

func (jm *JobManager) ProcessShare(share *types.Share) {
	//isValidBlock
	if share.BlockHex != "" {
		jm.DaemonManager.SubmitBlock(share.BlockHex)

		isAccepted, tx := jm.CheckBlockAccepted(share.BlockHex)
		share.TxHash = tx
		if isAccepted {
			go jm.Storage.PutShare(share)
			log.Println("Block Accepted: ")
		}

		gbt, err := jm.DaemonManager.GetBlockTemplate()
		if err != nil {
			panic(err)
		}
		jm.ProcessTemplate(gbt)
		return
	}

	// isValidShare
	if share.ErrorCode == 0 {
		go jm.Storage.PutShare(share)
		return
	}
}

func (jm *JobManager) CheckBlockAccepted(blockHash string) (isAccepted bool, tx string) {
	_, results := jm.DaemonManager.CmdAll("getblock", []interface{}{blockHash})
	if len(results) == 0 {
		return false, ""
	}

	isAccepted = true
	for i := range results {
		isAccepted = isAccepted && strings.Compare(daemonManager.BytesToGetBlock(results[i].Result).Hash, blockHash) == 0
	}

	if len(results) == 0 {
		return false, ""
	}

	for i := range results {
		gb := daemonManager.BytesToGetBlock(results[i].Result)
		if gb.Tx != nil {
			return isAccepted, gb.Tx[0]
		}
	}

	return isAccepted, ""
}

//func (jm *JobManager) UpdateCurrentJob(rpcData *daemonManager.GetBlockTemplate) {
//	tmpBlockTemplate := NewJob(
//		jm.JobCounter.Next(),
//		rpcData,
//		GetPoolAddressScript(jm.Options.Coin.Reward, jm.ValidateAddress),
//		jm.ExtraNoncePlaceholder,
//		jm.Options.Coin.Reward,
//		jm.Options.Coin.TxMessages,
//		jm.Options.RewardRecipients,
//	)
//
//	jm.CurrentJob = tmpBlockTemplate
//	//jm.UpdateBlockEvent <- tmpBlockTemplate
//	jm.ValidJobs[tmpBlockTemplate.JobId] = tmpBlockTemplate
//}

func (jm *JobManager) ProcessTemplate(rpcData *daemonManager.GetBlockTemplate) bool {
	isNewBlock := jm.CurrentJob == nil
	if !isNewBlock && strings.Compare(rpcData.PreviousBlockHash, jm.CurrentJob.GetBlockTemplate.PreviousBlockHash) != 0 {
		isNewBlock = true

		if rpcData.Height < jm.CurrentJob.GetBlockTemplate.Height {
			return false
		}
	}

	if !isNewBlock {
		return false
	}

	log.Println("New Block: ", string(utils.Jsonify(rpcData)))
	tmpBlockTemplate := NewJob(
		utils.RandHexUint64(),
		rpcData,
		GetPoolAddressScript(jm.Options.Coin.Reward, jm.ValidateAddress),
		jm.ExtraNoncePlaceholder,
		jm.Options.Coin.Reward,
		jm.Options.Coin.TxMessages,
		jm.Options.RewardRecipients,
	)

	jm.CurrentJob = tmpBlockTemplate
	jm.ValidJobs[tmpBlockTemplate.JobId] = tmpBlockTemplate

	return true
}

func (jm *JobManager) ProcessSubmit(jobId string, prevDiff, diff *big.Float, extraNonce1 []byte, hexExtraNonce2, hexNTime, hexNonce string, ipAddr net.Addr, workerName string) (ok bool, share *types.Share) {
	submitTime := time.Now()

	var miner, rig string
	names := strings.Split(workerName, ".")
	if len(names) < 2 {
		miner = names[0]
		rig = "unknown"
	} else {
		miner = names[0]
		rig = names[1]
	}

	job, exists := jm.ValidJobs[jobId]
	if !exists || job == nil || job.JobId != jobId {
		share = &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,
			ErrorCode:  20,
		}
		return false, share
	}

	extraNonce2, err := hex.DecodeString(hexExtraNonce2)
	if err != nil {
		log.Println(err)
	}

	if len(extraNonce2) != jm.ExtraNonce2Size {
		share = &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			ErrorCode: 21,
		}
		return false, share
	}

	if len(hexNTime) != 8 {
		share = &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			ErrorCode: 22,
		}
		return false, share
	}

	// allowed nTime range [GBT's CurTime, submitTime+7s]
	nTimeInt, err := strconv.ParseInt(hexNTime, 16, 64)
	if err != nil {
		log.Println(err)
	}
	if uint32(nTimeInt) < job.GetBlockTemplate.CurTime || nTimeInt > submitTime.Unix()+7 {
		return false, &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			ErrorCode: 23,
		}
	}

	if len(hexNonce) != 8 {
		return false, &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			ErrorCode: 24,
		}
	}

	if !job.RegisterSubmit(hex.EncodeToString(extraNonce1), hexExtraNonce2, hexNTime, hexNonce) {
		return false, &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			ErrorCode: 25,
		}
	}

	coinbaseBytes := job.SerializeCoinbase(extraNonce1, extraNonce2)
	coinbaseHash := jm.CoinbaseHasher(coinbaseBytes)
	merkleRoot := utils.ReverseBytes(job.MerkleTree.WithFirst(coinbaseHash))

	nonce, err := hex.DecodeString(hexNonce) // in big-endian
	if err != nil {
		log.Println(err)
	}

	nTimeBytes, err := hex.DecodeString(hexNTime) // in big-endian
	if err != nil {
		log.Println(err)
	}

	headerBytes := job.SerializeHeader(merkleRoot, nTimeBytes, nonce) // in LE
	headerHash := algorithm.Hash(headerBytes)
	headerHashBigInt := new(big.Int).SetBytes(utils.ReverseBytes(headerHash))

	bigShareDiff := new(big.Float).Quo(
		new(big.Float).SetInt(new(big.Int).Mul(algorithm.MaxTargetTruncated, big.NewInt(algorithm.Multiplier))),
		new(big.Float).SetInt(headerHashBigInt),
	)
	shareDiff, _ := bigShareDiff.Float64()

	//Check if share is a block candidate (matched network difficulty)
	if job.Target.Cmp(headerHashBigInt) > 0 {
		blockHex := hex.EncodeToString(job.SerializeBlock(headerBytes, coinbaseBytes))
		var blockHash string
		switch algorithm.Name {
		case "scrypt": // litecoin
			blockHash = hex.EncodeToString(utils.ReverseBytes(utils.Sha256d(headerBytes)))
		default:
			blockHash = hex.EncodeToString(utils.ReverseBytes(algorithm.Hash(headerBytes)))

		}

		log.Println("Found Block: " + blockHash)
		return true, &types.Share{
			JobId:      jobId,
			RemoteAddr: ipAddr,
			Miner:      miner,
			Rig:        rig,

			BlockHeight: job.GetBlockTemplate.Height,
			BlockReward: job.GetBlockTemplate.CoinbaseValue,
			Diff:        shareDiff,
			BlockHash:   blockHash,
			BlockHex:    blockHex,
		}
	}

	//Check if share didn't reached the miner's difficulty)
	if new(big.Float).Quo(bigShareDiff, diff).Cmp(big.NewFloat(0.99)) < 0 {
		//Check if share matched a previous difficulty from before a vardiff retarget
		if prevDiff != nil && bigShareDiff.Cmp(prevDiff) >= 0 {

			return true, &types.Share{
				JobId:      jobId,
				RemoteAddr: ipAddr,
				Miner:      miner,
				Rig:        rig,

				BlockHeight: job.GetBlockTemplate.Height,
				BlockReward: job.GetBlockTemplate.CoinbaseValue,
				Diff:        shareDiff,
			}
		} else {
			return false, &types.Share{
				JobId:      jobId,
				RemoteAddr: ipAddr,
				Miner:      workerName,
				ErrorCode:  26,
			}
		}
	}

	return true, &types.Share{
		JobId:      jobId,
		RemoteAddr: ipAddr,
		Miner:      miner,
		Rig:        rig,
	}
}

func GetPoolAddressScript(reward string, validateAddress *daemonManager.ValidateAddress) []byte {
	switch reward {
	case "POS":
		return utils.PublicKeyToScript(validateAddress.Pubkey)
	case "POW":
		if validateAddress.Isscript {
			return utils.P2SHAddressToScript(validateAddress.Address)
		}
		return utils.P2PKHAddressToScript(validateAddress.Address)
	default:
		// as POW
		log.Fatal("unknown reward type: " + reward)
		return nil
	}
}
