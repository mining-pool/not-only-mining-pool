// Package randomx binds RandomX v1.2.1 (tevador/RandomX): updated randomx.h,
// per-OS link flags (macOS does not support -static), light-mode VMs for
// pool-side share verification, and randomx_get_flags() exposure. Build
// lib/librandomx.a with build.sh (or `cmake .. && make` in a RandomX v1.2.1
// checkout).
package randomx

//#cgo CFLAGS: -I${SRCDIR}
//#cgo LDFLAGS: -L${SRCDIR}/lib -lrandomx -lstdc++
//#cgo linux LDFLAGS: -static -static-libgcc -static-libstdc++ -lpthread -lm
//#cgo windows LDFLAGS: -static -static-libgcc -static-libstdc++ -lpthread
//#cgo darwin LDFLAGS: -lm
/*
#include "randomx.h"
#include <time.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <pthread.h>

struct arg_struct {
	randomx_dataset *dataset;
	randomx_cache *cache;
	void *seed;
	uint32_t a, b;
};

void *init_full_dataset_thread(void *arguments)
{
	struct arg_struct *args = arguments;
	randomx_init_dataset(args->dataset, args->cache, args->a, args->b - args->a);
	free(arguments);
	pthread_exit(NULL);
	return NULL;
}

void init_full_dataset(randomx_dataset *dataset, randomx_cache *cache, uint32_t numThreads)
{
    const uint64_t datasetItemCount = randomx_dataset_item_count();

    if (numThreads > 1) {
		pthread_t threads[numThreads];

        for (uint64_t i = 0; i < numThreads; ++i) {
            pthread_t thread;
			threads[i] = thread;

            uint32_t a = (datasetItemCount * i) / numThreads;
            uint32_t b = (datasetItemCount * (i + 1)) / numThreads;

		    struct arg_struct *args = malloc(sizeof(struct arg_struct));
		    args->dataset = dataset;
		    args->cache = cache;
			args->a = a;
			args->b = b;

			pthread_create(&thread, NULL, &init_full_dataset_thread, (void *)args);
        }

        for (uint32_t i = 0; i < numThreads; ++i) {
            pthread_join(threads[i], NULL);
        }
    }
    else {
        randomx_init_dataset(dataset, cache, 0, datasetItemCount);
    }
}
*/
import "C"
import (
	"errors"
	"sync"
	"unsafe"
)

type Flag int

var (
	FlagDefault     Flag = 0 // for all default
	FlagLargePages  Flag = 1 // for dataset & rxCache & vm
	FlagHardAES     Flag = 2 // for vm
	FlagFullMEM     Flag = 4 // for vm
	FlagJIT         Flag = 8 // for vm & cache
	FlagSecure      Flag = 16
	FlagArgon2SSSE3 Flag = 32 // for cache
	FlagArgon2AVX2  Flag = 64 // for cache
	FlagArgon2      Flag = 96 // = avx2 + sse3
)

func (f Flag) toC() C.randomx_flags {
	return (C.randomx_flags)(f)
}

// GetFlags returns the recommended flags for the current machine (JIT,
// hardware AES, argon2 SIMD) as detected by randomx_get_flags(). Use these for
// cache and VM creation unless you have a reason not to.
func GetFlags() Flag {
	return Flag(C.randomx_get_flags())
}

func AllocCache(flags ...Flag) (*C.randomx_cache, error) {
	var SumFlag = FlagDefault
	var cache *C.randomx_cache

	for _, flag := range flags {
		SumFlag = SumFlag | flag
	}

	cache = C.randomx_alloc_cache(SumFlag.toC())
	if cache == nil {
		return nil, errors.New("failed to alloc mem for rxCache")
	}

	return cache, nil
}

func InitCache(cache *C.randomx_cache, seed []byte) {
	if len(seed) == 0 {
		panic("seed cannot be NULL")
	}

	C.randomx_init_cache(cache, unsafe.Pointer(&seed[0]), C.size_t(len(seed)))
}

func ReleaseCache(cache *C.randomx_cache) {
	C.randomx_release_cache(cache)
}

func AllocDataset(flags ...Flag) (*C.randomx_dataset, error) {
	var SumFlag = FlagDefault
	for _, flag := range flags {
		SumFlag = SumFlag | flag
	}

	var dataset *C.randomx_dataset
	dataset = C.randomx_alloc_dataset(SumFlag.toC())
	if dataset == nil {
		return nil, errors.New("failed to alloc mem for dataset")
	}

	return dataset, nil
}

func DatasetItemCount() uint32 {
	var length C.ulong
	length = C.randomx_dataset_item_count()
	return uint32(length)
}

func InitDataset(dataset *C.randomx_dataset, cache *C.randomx_cache, startItem uint32, itemCount uint32) {
	if dataset == nil {
		panic("alloc dataset mem is required")
	}

	if cache == nil {
		panic("alloc cache mem is required")
	}

	C.randomx_init_dataset(dataset, cache, C.ulong(startItem), C.ulong(itemCount))
}

// FastInitFullDataset using c's pthread to boost the dataset init. 472s -> 466s
func FastInitFullDataset(dataset *C.randomx_dataset, cache *C.randomx_cache, workerNum uint32) {
	if dataset == nil {
		panic("alloc dataset mem is required")
	}

	if cache == nil {
		panic("alloc cache mem is required")
	}

	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		C.init_full_dataset(dataset, cache, C.uint32_t(workerNum))
		wg.Done()
	}()

	wg.Wait()
}

func GetDatasetMemory(dataset *C.randomx_dataset) unsafe.Pointer {
	return C.randomx_get_dataset_memory(dataset)
}

func ReleaseDataset(dataset *C.randomx_dataset) {
	C.randomx_release_dataset(dataset)
}

func CreateVM(cache *C.randomx_cache, dataset *C.randomx_dataset, flags ...Flag) (*C.randomx_vm, error) {
	var SumFlag = FlagDefault
	for _, flag := range flags {
		SumFlag = SumFlag | flag
	}

	// A nil dataset is VALID for light-mode (verification) VMs: the RandomX C
	// API only requires a dataset when RANDOMX_FLAG_FULL_MEM is set. The old
	// unconditional panic made pool-side share verification impossible.
	if dataset == nil && SumFlag&FlagFullMEM != 0 {
		return nil, errors.New("FlagFullMEM requires an initialized dataset")
	}
	if cache == nil && SumFlag&FlagFullMEM == 0 {
		return nil, errors.New("light-mode vm requires an initialized cache")
	}

	vm := C.randomx_create_vm(SumFlag.toC(), cache, dataset)

	if vm == nil {
		return nil, errors.New("failed to create vm")
	}

	return vm, nil
}

func SetVMCache(vm *C.randomx_vm, cache *C.randomx_cache) {
	C.randomx_vm_set_cache(vm, cache)
}

func SetVMDataset(vm *C.randomx_vm, dataset *C.randomx_dataset) {
	C.randomx_vm_set_dataset(vm, dataset)
}

func DestroyVM(vm *C.randomx_vm) {
	C.randomx_destroy_vm(vm)
}

func CalculateHash(vm *C.randomx_vm, in []byte) []byte {
	out := make([]byte, C.RANDOMX_HASH_SIZE)
	if vm == nil {
		panic("failed hashing: using empty vm")
	}

	C.randomx_calculate_hash(vm, unsafe.Pointer(&in[0]), C.size_t(len(in)), unsafe.Pointer(&out[0]))
	return out
}

func CalculateHashFirst(vm *C.randomx_vm, in []byte) {
	if vm == nil {
		panic("failed hashing: using empty vm")
	}
	C.randomx_calculate_hash_first(vm, unsafe.Pointer(&in[0]), C.size_t(len(in)))
}

func CalculateHashNext(vm *C.randomx_vm, in []byte) []byte {
	out := make([]byte, C.RANDOMX_HASH_SIZE)
	if vm == nil {
		panic("failed hashing: using empty vm")
	}

	C.randomx_calculate_hash_next(vm, unsafe.Pointer(&in[0]), C.size_t(len(in)), unsafe.Pointer(&out[0]))
	return out
}

//// Types

type RxCache struct {
	seed      []byte
	cache     *C.randomx_cache
	initCount uint64
}

type RxDataset struct {
	dataset *C.randomx_dataset
	rxCache *RxCache

	workerNum uint32
}

type RxVM struct {
	vm        *C.randomx_vm
	rxDataset *RxDataset
}
