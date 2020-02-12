package jobManager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/node-standalone-pool/go-pool-server/algorithm"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"
)

type ExtraNonce1Generator struct {
	Size int
}

func NewExtraNonce1Generator() *ExtraNonce1Generator {
	return &ExtraNonce1Generator{
		Size: 2,
	}
}

func (eng *ExtraNonce1Generator) GetExtraNonce1() []byte {
	extraNonce := make([]byte, eng.Size)
	_, _ = rand.Read(extraNonce)

	return extraNonce
}

type JobCounter struct {
	Counter *big.Int
}

func NewJobCounter() *JobCounter {
	return &JobCounter{}
}

func (jc *JobCounter) Next() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	jc.Counter = new(big.Int).SetBytes(buf)
	return jc.Cur()
}

func (jc *JobCounter) Cur() string {
	return hex.EncodeToString(jc.Counter.Bytes())
}

type JobManager struct {
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

	ShareEvent    chan *Share
	NewBlockEvent chan *Job
}

func NewJobManager(options *config.Options, validateAddress *daemonManager.ValidateAddress) *JobManager {
	placeholder, _ := hex.DecodeString("f000000ff111111f")
	extraNonce1Generator := NewExtraNonce1Generator()

	return &JobManager{
		Options:               options,
		JobCounter:            NewJobCounter(),
		ExtraNonce1Generator:  extraNonce1Generator,
		ExtraNoncePlaceholder: placeholder,
		ExtraNonce2Size:       len(placeholder) - extraNonce1Generator.Size,
		CurrentJob:            nil,
		ValidJobs:             make(map[string]*Job),
		CoinbaseHasher:        utils.Sha256d,
		ValidateAddress:       validateAddress,
		ShareEvent:            make(chan *Share),
	}
}

func (jm *JobManager) Init(gbt *daemonManager.GetBlockTemplate) {
	jm.ProcessTemplate(gbt)

	// deal with share
	go func() {
		for {
			select {
			case share, ok := <-jm.ShareEvent:
				log.Println(share)

				if !ok {
					log.Println("share chan closed")
					return
				}

				//isValidShare := share.Error == nil
				isValidBlock := share.BlockHex != ""

				if isValidBlock {
					jm.DaemonManager.SubmitBlock(share.BlockHex)

					isAccepted, tx := jm.CheckBlockAccepted(share.BlockHex)
					if isAccepted {
						log.Println("Accepted")
					}

					share.TxHash = tx

					gbt, err := jm.DaemonManager.GetBlockTemplate()
					if err != nil {
						log.Fatal(err)
					}
					jm.ProcessTemplate(gbt)

					// TODO: add share to db
				}
			}
		}
	}()
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

	return isAccepted, daemonManager.BytesToGetBlock(results[0].Result).Tx[0]
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

	tmpBlockTemplate := NewJob(
		jm.JobCounter.Next(),
		rpcData,
		GetPoolAddressScript(jm.Options.Coin.Reward, jm.ValidateAddress),
		jm.ExtraNoncePlaceholder,
		jm.Options.Coin.Reward,
		jm.Options.Coin.TxMessages,
		jm.Options.RewardRecipients,
	)

	jm.CurrentJob = tmpBlockTemplate

	//jm.NewBlockEvent <- tmpBlockTemplate

	jm.ValidJobs[tmpBlockTemplate.JobId] = tmpBlockTemplate

	return true
}

type Share struct {
	JobId           string
	Ip              net.Addr
	Worker          string
	Difficulty      *big.Float
	Error           error
	BlockHash       string
	Height          int64
	BlockReward     uint64
	ShareDiff       *big.Float
	BlockDiff       *big.Float
	BlockDiffActual *big.Float
	BlockHex        string
	TxHash          string
}

func (jm *JobManager) ProcessShare(jobId string, previousDifficulty, difficulty *big.Float, extraNonce1 []byte, hexExtraNonce2, hexNTime, hexNonce string, ipAddress net.Addr, port int, workerName string) (ok bool, blockHash []byte, errParams *daemonManager.JsonRpcError) {
	submitTime := time.Now()

	extraNonce2, err := hex.DecodeString(hexExtraNonce2)
	if err != nil {
		log.Println(err)
	}

	if len(extraNonce2) != jm.ExtraNonce2Size {
		err := errors.New("incorrect size of extranonce2")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{
			Code:    20,
			Message: err.Error(),
		}
	}

	job := jm.ValidJobs[jobId]
	if job == nil || job.JobId != jobId {
		log.Println(jobId, "not in", jm.ValidJobs)
		err := errors.New("job not found")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{Code: 21, Message: err.Error()}
	}

	if len(hexNTime) != 8 {
		err := errors.New("incorrect size of ntime")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{Code: 20, Message: err.Error()}
	}

	nTimeInt, err := strconv.ParseInt(hexNTime, 16, 64)
	if err != nil {
		log.Println(err)
	}
	if nTimeInt < job.GetBlockTemplate.CurTime || nTimeInt > submitTime.Unix()+7 {
		err := errors.New("ntime out of range")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{Code: 20, Message: err.Error()}
	}

	if len(hexNonce) != 8 {
		err := errors.New("incorrect size of nonce")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{Code: 20, Message: err.Error()}
	}

	if !job.RegisterSubmit(hex.EncodeToString(extraNonce1), hexExtraNonce2, hexNTime, hexNonce) {
		err := errors.New("duplicate share")
		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      err,
		}

		return false, nil, &daemonManager.JsonRpcError{Code: 22, Message: err.Error()}
	}

	coinbaseBytes := job.SerializeCoinbase(extraNonce1, extraNonce2)
	coinbaseHash := jm.CoinbaseHasher(coinbaseBytes)
	merkleRoot := utils.ReverseBytes(job.MerkleTree.WithFirst(coinbaseHash))

	nonce, err := hex.DecodeString(hexNonce)
	if err != nil {
		log.Println(err)
	}

	nTimeBytes, err := hex.DecodeString(hexNTime) // in big-endian
	if err != nil {
		log.Println(err)
	}

	headerBytes := job.SerializeHeader(merkleRoot, nTimeBytes, nonce)
	headerHash := utils.ReverseBytes(algorithm.Hash(headerBytes))
	headerHashBigInt := new(big.Int).SetBytes(headerHash)

	shareDiff := new(big.Float).Quo(
		new(big.Float).SetInt(new(big.Int).Mul(algorithm.MaxTargetTruncated, big.NewInt(algorithm.Multiplier))),
		new(big.Float).SetInt(headerHashBigInt),
	)

	blockDiffAdjusted := new(big.Float).Mul(job.Difficulty, big.NewFloat(algorithm.Multiplier))

	//Check if share is a block candidate (matched network difficulty)
	if headerHashBigInt.Cmp(job.Target) <= 0 {
		blockHex := hex.EncodeToString(job.SerializeBlock(headerBytes, coinbaseBytes))
		blockHash := hex.EncodeToString(utils.ReverseBytes(algorithm.Hash(headerBytes)))

		jm.ShareEvent <- &Share{
			JobId:      jobId,
			Ip:         ipAddress,
			Worker:     workerName,
			Difficulty: difficulty,
			Error:      nil,

			Height:          job.GetBlockTemplate.Height,
			BlockReward:     job.GetBlockTemplate.CoinbaseValue,
			ShareDiff:       shareDiff,
			BlockDiff:       blockDiffAdjusted,
			BlockDiffActual: job.Difficulty,
			BlockHash:       blockHash,
			BlockHex:        blockHex,
		}

		log.Println("Found Block!")

		return true, []byte(blockHash), nil
	}

	//Check if share didn't reached the miner's difficulty)
	if new(big.Float).Quo(shareDiff, difficulty).Cmp(big.NewFloat(0.99)) < 0 {
		//Check if share matched a previous difficulty from before a vardiff retarget
		if previousDifficulty != nil && shareDiff.Cmp(previousDifficulty) >= 0 {
			difficulty = previousDifficulty

			jm.ShareEvent <- &Share{
				JobId:      jobId,
				Ip:         ipAddress,
				Worker:     workerName,
				Difficulty: difficulty,
				Error:      err,

				Height:          job.GetBlockTemplate.Height,
				BlockReward:     job.GetBlockTemplate.CoinbaseValue,
				ShareDiff:       shareDiff,
				BlockDiff:       blockDiffAdjusted,
				BlockDiffActual: job.Difficulty,
				//BlockHash: nil,
				//BlockHex: nil,
			}

			return true, nil, nil
		} else {
			err := errors.New("low difficulty share: " + shareDiff.String() + "/" + difficulty.String())
			jm.ShareEvent <- &Share{
				JobId:      jobId,
				Ip:         ipAddress,
				Worker:     workerName,
				Difficulty: difficulty,
				Error:      err,
			}
			return false, nil, &daemonManager.JsonRpcError{Code: 23, Message: err.Error()}
		}
	}

	jm.ShareEvent <- &Share{
		JobId:      jobId,
		Ip:         ipAddress,
		Worker:     workerName,
		Difficulty: difficulty,
		Error:      nil,
	}
	return true, nil, nil
}

func GetPoolAddressScript(reward string, validateAddress *daemonManager.ValidateAddress) []byte {
	switch reward {
	case "POS":
		return utils.PublicKeyToScript(validateAddress.Pubkey)
	case "POW":
		return utils.AddressToScript(validateAddress.Address)
	default:
		// as POW
		log.Fatal("unknown reward type: " + reward)
		return nil
	}
}
