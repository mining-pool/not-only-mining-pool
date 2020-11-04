package payments

import (
	"fmt"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/utils"
	"math"
	"strconv"
	"strings"
	"time"
)

var log = logging.Logger("payments")

type PayMode int

const (
	PayOnManual PayMode = iota
	PayPPLNS            // calc rewards on block found
	PayPPS              // calc rewards every day
)

type PaymentManager struct {
	options *config.PaymentOptions
	dm      *daemons.DaemonManager
	db      *storage.DB

	PoolAddress *config.Recipient
	Magnitude   float64
	MinPayment  uint64 // sat
}

func NewPaymentManager(options *config.PaymentOptions, poolAddr *config.Recipient, dm *daemons.DaemonManager, db *storage.DB) *PaymentManager {
	pm := &PaymentManager{
		options: options,
		dm:      dm,

		PoolAddress: poolAddr,
		Magnitude:   0,
		MinPayment:  0,
	}

	pm.Init()

	return pm
}

func (pm *PaymentManager) Init() {
	err := pm.validatePoolAddress()
	if err != nil {
		log.Panic(err)
	}

	err = pm.setMultiplier()
	if err != nil {
		log.Panic(err)
	}
}

func (pm *PaymentManager) Serve() {
	interval := time.NewTicker(time.Duration(pm.options.Interval) * time.Second)

	func() {
		for {
			<-interval.C
			err := pm.processPayments()
			if err != nil {
				log.Error("payment module exit: ", err)
				return
			}
		}
	}()
}

func (pm *PaymentManager) validatePoolAddress() error {
	// validate addr
	i, result, _, err := pm.dm.Cmd("getaddressinfo", []interface{}{pm.PoolAddress.Address}) // DEPRECATION WARNING: Parts of this command(validateaddress) have been deprecated and moved to getaddressinfo.
	if err != nil {
		return err
	}
	if result.Error != nil {
		return fmt.Errorf("error with payment processing daemon %s, err: %s", pm.dm.Daemons[i].String(), result.Error.Message)
	}

	va, err := daemons.BytesToValidateAddress(result.Result)
	if err != nil {
		return fmt.Errorf("error with payment processing daemon, err: %s", err)
	}

	if !va.IsMine {
		return fmt.Errorf("daemon %s does not own pool address, payment processing can not be done", pm.dm.Daemons[i].String())
	}

	return nil

}

func (pm *PaymentManager) setMultiplier() error {
	// validate balance
	i, result, _, err := pm.dm.Cmd("getbalance", []interface{}{})
	if err != nil {
		return err
	}

	if result.Error != nil {
		return fmt.Errorf("error with payment processing daemon %s, err: %s", pm.dm.Daemons[i].String(), result.Error.Message)
	}

	//gb, err := daemons.BytesToGetBalance(result.Result)
	//if err != nil {
	//	return fmt.Errorf("error with payment processing daemon %s, err: %s ", pm.dm.Daemons[i].String(), err)
	//}

	split := strings.Split(string(result.Result), ".")

	s := "1"
	for range split[1] {
		s += "0"
	}

	mul, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		log.Errorf("cannot parse %s. err: %s", s, err)
	}
	pm.Magnitude = float64(mul)
	pm.MinPayment = pm.CoinToSat(pm.options.MinPayment)

	return nil
}

//
func (pm *PaymentManager) processPayments() error {
	// PPLNS solution:
	//
	// for range // not wg => keep balance safe
	// validateaddress
	// getbalance
	// ['hgetall', coin + ':balances'],
	// ['smembers', coin + ':blocksPending']
	//
	// {
	//   blockHash: details[0],
	//   txHash: details[1],
	//   height: details[2],
	// };
	//
	// gettransaction generatiion tx & getaccount pooladdress -> chech tx detail -> kick/orphan/confirm
	//
	// ['hgetall', coin + ':shares:round' + r.height]
	// var reward = parseInt(round.reward * magnitude);
	//
	// sendmany [addressAccount, addressAmounts]
	//
	// ['hincrbyfloat', coin + ':balances', w, satoshisToCoins(Worker.balanceChange)]
	// ['hincrbyfloat', coin + ':payouts', w, Worker.sent]
	//
	// smove => move block from pending to kick/orphan/confirm

	workers, pbs, err := pm.readySend()
	if err != nil {
		return err
	}
	workers, pbs, err = pm.CalcRewards(workers, pbs)
	if err != nil {
		return err
	}

	pm.FinishPayment(workers, pbs)

	return nil
}

type PendingBlock struct {
	*storage.PendingBlock

	Category storage.BlockCategory // helper field for category
	Reward   float64               // unit: btc

	CanDeleteShares bool
	WorkerShares    map[string]float64
}

// Call redis to get an array of rounds - which are coinbase transactions and block heights from submitted
// blocks.
func (pm *PaymentManager) readySend() (map[string]*Worker, []*PendingBlock, error) {
	// init workers from balances
	f64Balances, err := pm.db.GetAllMinerBalances()
	workers := make(map[string]*Worker)
	for minerName, balance := range f64Balances {
		workers[minerName] = &Worker{
			Address:       minerName,
			Balance:       pm.CoinToSat(balance),
			Reward:        0,
			Sent:          0,
			BalanceChange: 0,
		}
	}

	pendingBlocks, err := pm.db.GetAllPendingBlocks()
	if err != nil {
		return nil, nil, err
	}

	batchCMD := make([]interface{}, 0, len(pendingBlocks))
	for i := range pendingBlocks {
		batchCMD = append(batchCMD, []interface{}{"gettransaction", []string{pendingBlocks[i].TxHash}})
	}

	//batchCMD = append(batchCMD, []interface{}{"getaccount", []string{pm.PoolAddress.Address}})

	_, results, err := pm.dm.BatchCmd(batchCMD)
	if err != nil {
		return nil, nil, err
	}

	pbs := make([]*PendingBlock, len(pendingBlocks))
	for i, result := range results {
		pbs[i].PendingBlock = pendingBlocks[i]

		if result.Error != nil && result.Error.Code == -5 {
			log.Warnf("Daemon reports invalid transaction: %s", pendingBlocks[i].TxHash)
			pbs[i].Category = storage.Kicked
			continue
		}

		gt, err := daemons.BytesToGetTransaction(result.Result)
		if err != nil {
			log.Error(err)
			continue
		}

		if gt.Details == nil || len(gt.Details) == 0 {
			log.Errorf("Daemon reports no details for transaction: %s", pendingBlocks[i].TxHash)
			continue
		}

		if result.Error != nil || result.Result == nil {
			log.Warnf("Odd error with gettransaction %v", result)
		}

		var isGenerationTx bool
		for i := range gt.Details {
			if gt.Details[i].Address == pm.PoolAddress.Address {
				isGenerationTx = true
				break
			}
		}

		if isGenerationTx {
			pbs[i].Category = storage.BlockCategory(gt.Details[0].Category)
		}

		if pbs[i].Category == storage.Generate {
			pbs[i].Reward = gt.Details[0].Amount // unit: btc
		}

	}

	for _, pb := range pbs {
		switch pb.Category {
		case storage.Orphan, storage.Kicked:
			//TODO delete shares
			pb.CanDeleteShares = canDeleteShares(pbs, pb)
		case storage.Generate:
		default:
			//
		}
	}

	return workers, pbs, nil
}

func canDeleteShares(rounds []*PendingBlock, r *PendingBlock) bool {
	for i := 0; i < len(rounds); i++ {
		var compareR = rounds[i]
		if compareR.Height == r.Height && compareR.Category != storage.Kicked && compareR.Category != storage.Orphan && compareR.Hash != r.Hash {
			return false
		}
	}

	return true
}

// Does a batch redis call to get shares contributed to each round. Then calculates the reward
// amount owned to each miner for each round(block).
func (pm *PaymentManager) CalcRewards(workers map[string]*Worker, pendingBlocks []*PendingBlock) (map[string]*Worker, []*PendingBlock, error) {
	for _, pb := range pendingBlocks {
		workerShares, err := pm.db.GetRoundContrib(pb.Height)
		if err != nil {
			return nil, nil, err
		}

		workerActualRewards := make(map[string]float64)

		switch pb.Category {
		case storage.Kicked, storage.Orphan:
			pb.WorkerShares = workerShares
		case storage.Generate:
			reward := pm.CoinToSat(pb.Reward)
			var totalShares = 0.0
			for _, share := range workerShares {
				totalShares = totalShares + share
			}

			for workerAddress, workerShare := range workerShares {
				percent := workerShare / totalShares
				workerActualRewards[workerAddress] = workerActualRewards[workerAddress] + math.Floor(float64(reward)*percent)
			}
		}
	}

	return workers, pendingBlocks, nil
}

type Worker struct {
	Address       string
	Balance       uint64  // sat
	Reward        uint64  // sat
	Sent          float64 // sat
	BalanceChange int     // sat
}

// Calculate if any payments are ready to be sent and trigger them sending
// Get balance different for each address and pass it along as object of latest balances such as
//{worker1: balance1, worker2, balance2}
//when deciding the sent balance, it the difference should be -1*(amount they had in db),
//if not sending the balance, the differnce should be +(the amount they earned this round)
func (pm *PaymentManager) trySend(workers map[string]*Worker, pendingBlocks []*PendingBlock, withholdPercent float64) (map[string]*Worker, []*PendingBlock, error) {
	var addressAmounts = map[string]float64{}
	var totalSent = uint64(0)
	for _, worker := range workers {
		var toSend = uint64(math.Floor(float64(worker.Balance+worker.Reward) * (1 - withholdPercent)))
		if toSend >= pm.MinPayment {
			totalSent += toSend
			var address = worker.Address
			addressAmounts[address] = pm.SatToCoin(toSend)
			worker.Sent = addressAmounts[address]
			worker.BalanceChange = int(u64Min(worker.Balance, toSend)) * -1
		} else {
			worker.BalanceChange = int(u64Max(toSend-worker.Balance, 0))
			worker.Sent = 0
		}
	}

	if len(addressAmounts) == 0 {
		return workers, pendingBlocks, nil
	}

	_, result, _, err := pm.dm.Cmd("sendmany", []interface{}{"", addressAmounts}) // use default "" account (not addr)
	if err != nil {
		return nil, nil, err
	}

	//Check if payments failed because wallet doesn't have enough coins to pay for tx fees
	if result.Error != nil && result.Error.Code == -6 {
		var higherPercent = withholdPercent + 0.01
		log.Warn("Not enough funds to cover the tx fees for sending out payments, decreasing rewards by ", higherPercent*100, "% and retrying")
		return pm.trySend(workers, pendingBlocks, higherPercent)
	}

	if result.Error != nil {
		log.Error("Error trying to send payments with RPC sendmany ", utils.Jsonify(result.Error))
		return nil, nil, err
	}
	log.Debug("Sent out a total of ", pm.SatToCoin(totalSent), " to ", len(addressAmounts), " workers")
	if withholdPercent > 0 {
		log.Warn("Had to withhold ", withholdPercent*100, "% of reward from miners to cover transaction fees. Fund pool wallet with coins to prevent this from happening")
	}

	return workers, pendingBlocks, nil

}

func (pm *PaymentManager) FinishPayment(workers map[string]*Worker, pds []*PendingBlock) {
	pm.trySend(workers, pds, 0)
}

func (pm *PaymentManager) SatToCoin(sat uint64) float64 {
	return float64(sat) / pm.Magnitude
}

func (pm *PaymentManager) CoinToSat(coin float64) uint64 {
	return uint64(math.Floor(coin * pm.Magnitude))
}

func u64Min(x, y uint64) uint64 {
	if x < y {
		return x
	}

	return y
}

func u64Max(x, y uint64) uint64 {
	if x < y {
		return y
	}

	return x
}
