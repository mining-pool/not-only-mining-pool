package vardiff

import (
	"github.com/mining-pool/go-pool-server/config"
	"time"
)

type VarDiff struct {
	Options       *config.VarDiffOptions
	BufferSize    int64
	MaxTargetTime float64
	MinTargetTime float64

	TimeBuffer    *RingBuffer
	LastRtc       int64
	LastTimestamp int64
}

func NewVarDiff(options *config.VarDiffOptions) *VarDiff {
	timestamp := time.Now().Unix()
	bufferSize := options.RetargetTime / options.TargetTime * 4
	return &VarDiff{
		Options:       options,
		BufferSize:    bufferSize,
		MaxTargetTime: float64(options.TargetTime) * (1 + options.VariancePercent),
		MinTargetTime: float64(options.TargetTime) * (1 - options.VariancePercent),
		TimeBuffer:    NewRingBuffer(bufferSize),
		LastRtc:       timestamp - options.RetargetTime/2,
		LastTimestamp: timestamp,
	}
}

//func (vd *VarDiff) ManagePort(, ) {
//	stratumPort := client.Socket.LocalAddr().(*net.TCPAddr).Port
//}

// On SubmitEvent
// then client.EnqueueNextDifficulty(newDiff)
func (vd *VarDiff) CalcNextDiff(currentDiff float64) (newDiff float64) {
	timestamp := time.Now().Unix()

	if vd.LastRtc == 0 {
		vd.LastRtc = timestamp - vd.Options.RetargetTime/2
		vd.LastTimestamp = timestamp
		return
	}

	sinceLast := timestamp - vd.LastTimestamp

	vd.TimeBuffer.Append(sinceLast)
	vd.LastTimestamp = timestamp

	if (timestamp-vd.LastRtc) < vd.Options.RetargetTime && vd.TimeBuffer.Size() > 0 {
		return
	}

	vd.LastRtc = timestamp
	avg := vd.TimeBuffer.Avg()
	ddiff := float64(time.Duration(vd.Options.TargetTime)*time.Second) / avg

	//currentDiff, _ := client.Difficulty.Float64()

	if avg > vd.MaxTargetTime && currentDiff > vd.Options.MinDiff {
		if vd.Options.X2Mode {
			ddiff = 0.5
		}

		if ddiff*currentDiff < vd.Options.MinDiff {
			ddiff = vd.Options.MinDiff / currentDiff
		}
	} else if avg < vd.MinTargetTime {
		if vd.Options.X2Mode {
			ddiff = 2
		}

		diffMax := vd.Options.MaxDiff

		if ddiff*currentDiff > diffMax {
			ddiff = diffMax / currentDiff
		}
	} else {
		return currentDiff
	}

	newDiff = currentDiff * ddiff

	if newDiff <= 0 {
		newDiff = currentDiff
	}

	vd.TimeBuffer.Clear()
	return
}
