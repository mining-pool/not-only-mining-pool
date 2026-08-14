package randomx

import "C"
import (
	"fmt"
	"sync"
)

func NewRxDataset(flags ...Flag) (*RxDataset, error) {
	cache, err := NewRxCache(flags...)
	if err != nil {
		return nil, err
	}

	dataset, err := AllocDataset(flags...)
	if err != nil {
		return nil, err
	}

	return &RxDataset{
		dataset: dataset,
		rxCache: cache,

		workerNum: 1,
	}, nil
}

func (ds *RxDataset) Close() {
	if ds.dataset != nil {
		ReleaseDataset(ds.dataset)
	}

	ds.rxCache.Close()
}

func (ds *RxDataset) GoInit(seed []byte, workerNum uint32) bool {
	if ds.rxCache.Init(seed) == false {
		fmt.Println("WARN: rxCache has already been initialized by the same seed")
	}

	if ds.rxCache == nil || ds.rxCache.cache == nil {
		return false
	}

	datasetItemCount := DatasetItemCount()
	var wg sync.WaitGroup

	for i := uint32(0); i < workerNum; i++ {
		a := (datasetItemCount * i) / workerNum
		b := (datasetItemCount * (i + 1)) / workerNum
		wg.Add(1)
		go func() {
			InitDataset(ds.dataset, ds.rxCache.cache, a, b-a)
			wg.Done()
		}()
	}
	wg.Wait()

	return true
}

func (ds *RxDataset) CInit(seed []byte, workerNum uint32) bool {
	if ds.rxCache.Init(seed) == false {
		fmt.Println("WARN: rxCache has already been initialized by the same seed")
	}

	if ds.rxCache == nil || ds.rxCache.cache == nil {
		return false
	}

	FastInitFullDataset(ds.dataset, ds.rxCache.cache, workerNum)

	return true
}
