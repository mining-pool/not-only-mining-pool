package payments

import (
	"fmt"
	"math"
	"strings"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/storage"
)

var log = logging.Logger("payments")

// PaymentManager pays miners proportionally to the shares they contributed to
// each block round (PROP). It is fork-configurable via config.PaymentOptions so
// one binary pays out on any bitcoind-family fork whose wallet RPC differs.
type PaymentManager struct {
	options *config.PaymentOptions
	dm      *daemons.DaemonManager
	db      *storage.DB

	PoolAddress *config.Recipient
	Magnitude   float64 // base units (satoshis) per coin
	MinPayment  uint64  // satoshis
}

func NewPaymentManager(options *config.PaymentOptions, poolAddr *config.Recipient, dm *daemons.DaemonManager, db *storage.DB) *PaymentManager {
	pm := &PaymentManager{
		dm:          dm,
		db:          db,
		PoolAddress: poolAddr,
	}
	// options is nil when payments are disabled; Init/Serve are then never called.
	if options != nil {
		pm.options = options.WithDefaults()
	}
	return pm
}

// Init validates the pay mode, the pool address ownership, and the coin
// precision. It must be called (once) before Serve, only when payments are on.
func (pm *PaymentManager) Init() error {
	switch pm.options.PayMode {
	case config.PayModeProp, config.PayModePPLNS, config.PayModeSolo:
	case config.PayModePPS:
		if pm.options.PPSRate <= 0 {
			return fmt.Errorf("payMode %q requires a positive ppsRate", config.PayModePPS)
		}
	default:
		return fmt.Errorf("unsupported payMode %q (want prop, pplns, solo or pps)", pm.options.PayMode)
	}
	if err := pm.validatePoolAddress(); err != nil {
		return err
	}
	return pm.setMagnitude()
}

// attribute splits a mature block's reward across miners per the configured
// payMode, returning miner -> reward (satoshis). An empty result means the block
// cannot be attributed (no shares / unknown finder) and should be orphaned.
func (pm *PaymentManager) attribute(pb *storage.PendingBlock, rewardSat uint64) (map[string]uint64, error) {
	switch pm.options.PayMode {
	case config.PayModeSolo:
		if pb.Finder == "" {
			return nil, nil
		}
		return map[string]uint64{pb.Finder: rewardSat}, nil
	case config.PayModePPLNS:
		shares, err := pm.db.GetPPLNSShares(pb.Mark, pm.options.PPLNSWindow)
		if err != nil {
			return nil, err
		}
		return splitByShares(rewardSat, shares), nil
	default: // prop
		shares, err := pm.db.GetRoundContrib(pb.Height)
		if err != nil {
			return nil, err
		}
		return splitByShares(rewardSat, shares), nil
	}
}

// splitByShares divides rewardSat proportionally to each miner's share weight.
func splitByShares(rewardSat uint64, shares map[string]float64) map[string]uint64 {
	var total float64
	for _, s := range shares {
		total += s
	}
	if total <= 0 {
		return nil
	}
	out := make(map[string]uint64, len(shares))
	for miner, s := range shares {
		out[miner] = uint64(math.Floor(float64(rewardSat) * (s / total)))
	}
	return out
}

func (pm *PaymentManager) Serve() {
	ticker := time.NewTicker(time.Duration(pm.options.Interval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := pm.processPayments(); err != nil {
			log.Error("payment run failed: ", err)
		}
	}
}

// validatePoolAddress confirms the payment wallet owns the pool payout address,
// so its coinbase rewards land somewhere the pool can spend from.
func (pm *PaymentManager) validatePoolAddress() error {
	instance, result, _ := pm.dm.Cmd(pm.options.AddressCheckMethod, []interface{}{pm.PoolAddress.Address})
	if result == nil {
		return fmt.Errorf("no response from payment daemon on %s", pm.options.AddressCheckMethod)
	}
	if result.Error != nil {
		return fmt.Errorf("payment daemon %s: %s", instance.String(), result.Error.Message)
	}

	va, err := daemons.BytesToValidateAddress(result.Result)
	if err != nil {
		return err
	}
	if !va.IsMine {
		return fmt.Errorf("payment daemon %s does not own pool address %s; payouts impossible", instance.String(), pm.PoolAddress.Address)
	}
	return nil
}

// setMagnitude fixes the base-units-per-coin factor. It honours an explicit
// config value and otherwise derives it from the wallet's getbalance precision.
func (pm *PaymentManager) setMagnitude() error {
	if pm.options.Magnitude > 0 {
		pm.Magnitude = pm.options.Magnitude
	} else {
		instance, result, _ := pm.dm.Cmd("getbalance", []interface{}{})
		if result == nil {
			return fmt.Errorf("no response from payment daemon on getbalance")
		}
		if result.Error != nil {
			return fmt.Errorf("payment daemon %s: %s", instance.String(), result.Error.Message)
		}
		decimals := 0
		if parts := strings.SplitN(strings.TrimSpace(string(result.Result)), ".", 2); len(parts) == 2 {
			decimals = len(strings.TrimRight(parts[1], "\n\r "))
		}
		if decimals == 0 {
			decimals = 8 // safe bitcoin default when the balance is a whole number
		}
		pm.Magnitude = math.Pow10(decimals)
	}
	pm.MinPayment = pm.CoinToSat(pm.options.MinPayment)
	log.Infof("payments: magnitude=%.0f min=%d sat interval=%ds maturity=%d", pm.Magnitude, pm.MinPayment, pm.options.Interval, pm.options.MinConfirmations)
	return nil
}

// worker accumulates one miner's owed amount across a payout run.
type worker struct {
	Address string
	Balance uint64 // carried-over unpaid balance (satoshis)
	Reward  uint64 // rewards earned this run (satoshis)
	Sent    uint64 // paid out this run (satoshis)
}

// matureBlock is a pending block whose reward is confirmed and ready to split.
type matureBlock struct {
	block  *storage.PendingBlock
	reward uint64 // satoshis credited to the pool address
}

// processPayments runs one payout cycle: classify pending blocks by their
// coinbase transaction (orphan / immature / mature), split each mature block's
// reward across that round's shares, and pay every miner over the threshold —
// persisting balances, payouts and block state only if sendmany succeeds.
func (pm *PaymentManager) processPayments() error {
	if pm.options.PayMode == config.PayModePPS {
		return pm.processPPS()
	}

	pendingBlocks, err := pm.db.GetAllPendingBlocks()
	if err != nil {
		return err
	}
	if len(pendingBlocks) == 0 {
		return nil
	}

	update := &storage.PaymentUpdate{Balances: map[string]float64{}, Paid: map[string]float64{}}
	var matured []matureBlock

	// seed workers from carried-over balances
	balances, err := pm.db.GetAllMinerBalances()
	if err != nil {
		return err
	}
	workers := make(map[string]*worker, len(balances))
	for miner, bal := range balances {
		workers[miner] = &worker{Address: miner, Balance: pm.CoinToSat(bal)}
	}

	for _, pb := range pendingBlocks {
		reward, category, confirmations, ok := pm.classifyBlock(pb)
		if !ok {
			continue // transient RPC problem; retry this block next run
		}
		switch {
		case category == string(storage.Orphan) || category == string(storage.Kicked):
			update.Orphaned = append(update.Orphaned, pb.String())
			update.DeleteRounds = append(update.DeleteRounds, pb.Height)
		case confirmations < pm.options.MinConfirmations:
			// fewer than minConfirmations — leave it pending, revisit next run. An
			// "immature" coinbase is just a young one; sendmany funds payouts from
			// any mature wallet UTXO, so minConfirmations alone governs distribution.
		default: // enough confirmations — distribute
			rewardSat := pm.CoinToSat(reward)
			dist, err := pm.attribute(pb, rewardSat)
			if err != nil {
				return err
			}
			if len(dist) == 0 {
				// matured but nothing to attribute (no shares / unknown finder):
				// orphan it so we don't leak the reward or loop forever.
				update.Orphaned = append(update.Orphaned, pb.String())
				update.DeleteRounds = append(update.DeleteRounds, pb.Height)
				continue
			}
			for miner, r := range dist {
				w := workers[miner]
				if w == nil {
					w = &worker{Address: miner}
					workers[miner] = w
				}
				w.Reward += r
			}
			matured = append(matured, matureBlock{block: pb, reward: rewardSat})
		}
	}

	if len(matured) == 0 {
		// nothing to pay this run; still persist orphan moves so we don't rescan them.
		if len(update.Orphaned) == 0 {
			return nil
		}
		return pm.db.ApplyPayments(update)
	}

	if err := pm.settle(workers, update, 0); err != nil {
		return err // sendmany failed — persist nothing, blocks stay pending for retry
	}
	for _, mb := range matured {
		update.Confirmed = append(update.Confirmed, mb.block.String())
		update.DeleteRounds = append(update.DeleteRounds, mb.block.Height)
	}
	return pm.db.ApplyPayments(update)
}

// processPPS pays a fixed rate per share: it credits every share logged since
// the cursor (reward = difficulty * ppsRate), pays balances over the threshold,
// and confirms matured blocks — whose rewards refill the pool wallet rather than
// being distributed (the pool, not the miners, carries the luck variance).
func (pm *PaymentManager) processPPS() error {
	cursor, err := pm.db.GetPPSCursor()
	if err != nil {
		return err
	}
	newShares, maxSeq, err := pm.db.GetSharesSince(cursor)
	if err != nil {
		return err
	}

	balances, err := pm.db.GetAllMinerBalances()
	if err != nil {
		return err
	}
	workers := make(map[string]*worker, len(balances))
	for miner, bal := range balances {
		workers[miner] = &worker{Address: miner, Balance: pm.CoinToSat(bal)}
	}
	for miner, diff := range newShares {
		w := workers[miner]
		if w == nil {
			w = &worker{Address: miner}
			workers[miner] = w
		}
		w.Reward += pm.CoinToSat(diff * pm.options.PPSRate)
	}

	update := &storage.PaymentUpdate{Balances: map[string]float64{}, Paid: map[string]float64{}, PPSCursor: maxSeq}

	// Move matured blocks out of pending (their reward funds the wallet, so it is
	// not split); orphan the rest. Nothing here is distributed to miners.
	pendingBlocks, err := pm.db.GetAllPendingBlocks()
	if err != nil {
		return err
	}
	for _, pb := range pendingBlocks {
		_, category, confirmations, ok := pm.classifyBlock(pb)
		if !ok {
			continue
		}
		switch {
		case category == string(storage.Orphan) || category == string(storage.Kicked):
			update.Orphaned = append(update.Orphaned, pb.String())
			update.DeleteRounds = append(update.DeleteRounds, pb.Height)
		case confirmations < pm.options.MinConfirmations:
			// fewer than minConfirmations — leave pending until mature
		default:
			update.Confirmed = append(update.Confirmed, pb.String())
			update.DeleteRounds = append(update.DeleteRounds, pb.Height)
		}
	}

	if err := pm.settle(workers, update, 0); err != nil {
		return err // payout failed — leave the cursor/blocks so we retry next run
	}
	return pm.db.ApplyPayments(update)
}

// classifyBlock looks up a pending block's coinbase transaction and reports the
// reward credited to the pool address, its category and its confirmations. ok is
// false only on a transient RPC failure (retry next run).
func (pm *PaymentManager) classifyBlock(pb *storage.PendingBlock) (reward float64, category string, confirmations int64, ok bool) {
	_, result, _ := pm.dm.Cmd("gettransaction", []interface{}{pb.TxHash})
	if result == nil {
		return 0, "", 0, false
	}
	if result.Error != nil {
		if result.Error.Code == -5 { // tx not in wallet: the block never made it in
			return 0, string(storage.Orphan), 0, true
		}
		log.Warnf("gettransaction %s: %s", pb.TxHash, result.Error.Message)
		return 0, "", 0, false
	}

	gt, err := daemons.BytesToGetTransaction(result.Result)
	if err != nil {
		log.Error(err)
		return 0, "", 0, false
	}

	// find the detail crediting the pool address (the coinbase output).
	category = string(storage.Generate)
	reward = gt.Amount
	for i := range gt.Details {
		if gt.Details[i].Address == pm.PoolAddress.Address {
			category = gt.Details[i].Category
			reward = gt.Details[i].Amount
			break
		}
	}
	return reward, category, int64(gt.Confirmations), true
}

// settle computes each worker's payout, calls sendmany, and (on success) fills
// the update with the new balances and payouts. On -6 (insufficient funds for
// fees) it retries withholding a little more so miners cover the tx fee.
func (pm *PaymentManager) settle(workers map[string]*worker, update *storage.PaymentUpdate, withhold float64) error {
	amounts := map[string]float64{}
	for _, w := range workers {
		owed := w.Balance + w.Reward
		toSend := uint64(math.Floor(float64(owed) * (1 - withhold)))
		if toSend >= pm.MinPayment && toSend > 0 {
			amounts[w.Address] = pm.SatToCoin(toSend)
			w.Sent = toSend
		} else {
			w.Sent = 0
		}
	}

	if len(amounts) > 0 {
		args := []interface{}{}
		if !pm.options.OmitSendManyDummy {
			args = append(args, pm.options.SendManyDummy)
		}
		args = append(args, amounts)

		_, result, _ := pm.dm.Cmd("sendmany", args)
		if result == nil {
			return fmt.Errorf("no response from payment daemon on sendmany")
		}
		if result.Error != nil {
			if result.Error.Code == -6 { // not enough funds to also cover the fee
				next := withhold + 0.01
				if next >= 1 {
					return fmt.Errorf("wallet cannot cover sendmany fees even at 100%% withholding")
				}
				log.Warnf("insufficient funds for fees; retrying with %.0f%% withheld", next*100)
				return pm.settle(workers, update, next)
			}
			return fmt.Errorf("sendmany failed: %s", result.Error.Message)
		}
		if withhold > 0 {
			log.Warnf("paid %d workers (withheld %.0f%% for fees; fund the wallet to avoid this)", len(amounts), withhold*100)
		} else {
			log.Infof("paid %d workers", len(amounts))
		}
	}

	// carry the unpaid remainder forward and record what was paid.
	for _, w := range workers {
		newBalance := (w.Balance + w.Reward) - w.Sent
		update.Balances[w.Address] = pm.SatToCoin(newBalance)
		if w.Sent > 0 {
			update.Paid[w.Address] = pm.SatToCoin(w.Sent)
		}
	}
	return nil
}

func (pm *PaymentManager) SatToCoin(sat uint64) float64 {
	return float64(sat) / pm.Magnitude
}

func (pm *PaymentManager) CoinToSat(coin float64) uint64 {
	if coin <= 0 {
		return 0
	}
	return uint64(math.Floor(coin*pm.Magnitude + 0.5))
}
