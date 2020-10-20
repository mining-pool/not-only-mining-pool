package payments

import (
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/daemons"
)

var log = logging.Logger("payments")

type PayMode int

const (
	PayOnManual PayMode = iota
	PayPPLNS            // calc rewards on block found
	PayPPS              // calc rewards every day
)

type PaymentManager struct {
	pay chan struct{}
	dm  *daemons.DaemonManager
}

func NewPaymentManager(mode PayMode, dm *daemons.DaemonManager) *PaymentManager {
	return &PaymentManager{
		pay: make(chan struct{}),
		dm:  dm,
	}
}

func (pm *PaymentManager) Serve() {
	func() {
		for {
			<-pm.pay
			pm.doPay()
		}
	}()
}

func (pm *PaymentManager) doPay() {
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
	// ['hincrbyfloat', coin + ':balances', w, satoshisToCoins(worker.balanceChange)]
	// ['hincrbyfloat', coin + ':payouts', w, worker.sent]
	//
	// smove => move block from pending to kick/orphan/confirm
}
