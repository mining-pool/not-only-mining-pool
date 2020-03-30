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
	PoolAddress *config.Recipient

	Storage               *storageManager.Storage
	Options               *config.Options
	JobCounter            *JobCounter
	ExtraNonce1Generator  *ExtraNonce1Generator
	ExtraNoncePlaceholder []byte
	ExtraNonce2Size       int

	CurrentJob *Job
	ValidJobs  map[string]*Job

	CoinbaseHasher func([]byte) []byte

	DaemonManager *daemonManager.DaemonManager

	NewBlockEvent chan *Job
}

func NewJobManager(options *config.Options, dm *daemonManager.DaemonManager, storage *storageManager.Storage) *JobManager {
	placeholder, _ := hex.DecodeString("f000000ff111111f")
	extraNonce1Generator := NewExtraNonce1Generator()

	return &JobManager{
		PoolAddress: options.PoolAddress,

		Options:               options,
		ExtraNonce1Generator:  extraNonce1Generator,
		ExtraNoncePlaceholder: placeholder,
		ExtraNonce2Size:       len(placeholder) - extraNonce1Generator.Size,
		CurrentJob:            nil,
		ValidJobs:             make(map[string]*Job),
		CoinbaseHasher:        utils.Sha256d,
		Storage:               storage,
		DaemonManager:         dm,
	}
}

func (jm *JobManager) Init(gbt *daemonManager.GetBlockTemplate) {
	jm.ProcessTemplate(gbt)
}

func (jm *JobManager) ProcessShare(share *types.Share) {
	//isValidBlock
	if share.BlockHex != "" {
		log.Printf("submitting new Block: %s", share.BlockHex)
		jm.DaemonManager.SubmitBlock(share.BlockHex)

		isAccepted, tx := jm.CheckBlockAccepted(share.BlockHex)
		share.TxHash = tx
		if isAccepted {
			go jm.Storage.PutShare(share)
			log.Printf("Block %s Accepted! tx: %s. Wait for pendding!", share.BlockHex, share.TxHash)
		}

		gbt, err := jm.DaemonManager.GetBlockTemplate()
		if err != nil {
			panic(err)
		}
		jm.ProcessTemplate(gbt)
		return
	} else if share.ErrorCode == 0 {
		// notValidBlock but isValidShare
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

// Update updates the job when mining the same height but tx changes
func (jm *JobManager) UpdateCurrentJob(rpcData *daemonManager.GetBlockTemplate) {
	tmpBlockTemplate := NewJob(
		jm.CurrentJob.JobId,
		rpcData,
		jm.PoolAddress.GetScript(),
		jm.ExtraNoncePlaceholder,
		jm.Options.Coin.Reward,
		jm.Options.Coin.TxMessages,
		jm.Options.RewardRecipients,
	)

	jm.CurrentJob = tmpBlockTemplate
	jm.ValidJobs[tmpBlockTemplate.JobId] = tmpBlockTemplate

	log.Println("Job updated")
}

// CreateNewJob creates a new job when mining new height
func (jm *JobManager) CreateNewJob(rpcData *daemonManager.GetBlockTemplate) {
	// creates a new job when mining new height

	tmpBlockTemplate := NewJob(
		utils.RandHexUint64(),
		rpcData,
		jm.PoolAddress.GetScript(),
		jm.ExtraNoncePlaceholder,
		jm.Options.Coin.Reward,
		jm.Options.Coin.TxMessages,
		jm.Options.RewardRecipients,
	)

	jm.CurrentJob = tmpBlockTemplate
	jm.ValidJobs[tmpBlockTemplate.JobId] = tmpBlockTemplate

	log.Println("New Job (Block) from blocktemplate")
}

// ProcessTemplate handles the template
func (jm *JobManager) ProcessTemplate(rpcData *daemonManager.GetBlockTemplate) {
	if jm.CurrentJob != nil && rpcData.Height < jm.CurrentJob.GetBlockTemplate.Height {
		return
	}

	if jm.CurrentJob != nil && rpcData.Height == jm.CurrentJob.GetBlockTemplate.Height {
		jm.UpdateCurrentJob(rpcData)
		return
	}

	jm.CreateNewJob(rpcData)
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
	headerHash := algorithm.GetHashFunc(jm.Options.Algorithm.Name)(headerBytes)
	headerHashBigInt := new(big.Int).SetBytes(utils.ReverseBytes(headerHash))

	bigShareDiff := new(big.Float).Quo(
		new(big.Float).SetInt(new(big.Int).Mul(algorithm.MaxTargetTruncated, big.NewInt(1<<jm.Options.Algorithm.Multiplier))),
		new(big.Float).SetInt(headerHashBigInt),
	)
	shareDiff, _ := bigShareDiff.Float64()

	//Check if share is a block candidate (matched network difficulty)
	if job.Target.Cmp(headerHashBigInt) > 0 {
		blockHex := hex.EncodeToString(job.SerializeBlock(headerBytes, coinbaseBytes))
		var blockHash string
		if jm.Options.Algorithm.SHA256dBlockHasher {
			// LTC
			blockHash = hex.EncodeToString(utils.ReverseBytes(utils.Sha256d(headerBytes)))
		} else {
			// DASH
			blockHash = hex.EncodeToString(utils.ReverseBytes(algorithm.GetHashFunc(jm.Options.Algorithm.Name)(headerBytes)))
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

//func GetPoolAddressScript(reward string, validateAddress *daemonManager.ValidateAddress) []byte {
//	switch reward {
//	case "POS":
//		return utils.PublicKeyToScript(validateAddress.Pubkey)
//	case "POW":
//		if validateAddress.Isscript {
//			return utils.P2SHAddressToScript(validateAddress.Address)
//		}
//		return utils.P2PKHAddressToScript(validateAddress.Address)
//	default:
//		// as POW
//		log.Fatal("unknown reward type: " + reward)
//		return nil
//	}
//}
