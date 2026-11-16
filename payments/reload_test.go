package payments

import (
	"testing"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

func TestPaymentSettingsApplyBetweenRuns(t *testing.T) {
	pm := NewPaymentManager(&config.PaymentOptions{Interval: 600, MinPayment: 1, MinConfirmations: 100, PayMode: "prop"}, nil, nil, nil)
	pm.Magnitude = 1e8
	pm.MinPayment = pm.CoinToSat(1)

	pm.Update(&config.PaymentOptions{Interval: 60, MinPayment: 0.5, MinConfirmations: 30, PayMode: "pps", PPSRate: 9})
	if pm.options.MinConfirmations != 100 || pm.MinPayment != 1e8 {
		t.Fatal("settings changed before the next run")
	}
	select {
	case d := <-pm.interval:
		if d != time.Minute {
			t.Fatalf("interval %v", d)
		}
	default:
		t.Fatal("interval change not signalled to the scheduler")
	}

	pm.applyPending()
	if pm.options.Interval != 60 || pm.options.MinConfirmations != 30 || pm.MinPayment != 5e7 {
		t.Fatalf("not applied: %+v min=%d", pm.options, pm.MinPayment)
	}
	if pm.options.PayMode != "prop" || pm.options.PPSRate != 0 {
		t.Fatal("pay mode and accounting parameters must not change at runtime")
	}
	pm.applyPending() // nothing pending: no-op
	if pm.options.Interval != 60 {
		t.Fatal("applyPending without an update changed settings")
	}
}
