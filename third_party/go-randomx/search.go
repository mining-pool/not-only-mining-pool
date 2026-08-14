package randomx

/*
#include <stdint.h>
#include <stdbool.h>
#include "randomx.h"

bool search(randomx_vm* vm, void* in, const uint64_t target, const uint64_t max_times, const uint32_t jump, void* nonce, void* out, void* sol)
{
	//randomx_calculate_hash_first(vm, in, 76);

    for (uint64_t i=0; i < max_times; i++)
	{
		*(uint32_t*)(in+39) = *(uint32_t*)(nonce) + jump;
		randomx_calculate_hash_next(vm, in, 76, out);

		*(uint32_t*)(sol) = *(uint32_t*)(nonce);
		*(uint32_t*)(nonce) = *(uint32_t*)(in+39);

		if (*(uint64_t*)(out+24) < target) {
			return true;
		}
	}

	return false;
}
*/
import "C"
import (
	"unsafe"
)

func Search(vm *C.randomx_vm, in []byte, target uint64, maxTimes uint64, jump uint32, nonce []byte) (Hash []byte, Found bool, Sol []byte) {
	hash := make([]byte, C.RANDOMX_HASH_SIZE)
	sol := make([]byte, 4)
	if vm == nil {
		panic("failed hashing: using empty vm")
	}

	var cFound C.bool
	cFound = C.search(vm, unsafe.Pointer(&in[0]), C.uint64_t(target), C.uint64_t(maxTimes), C.uint32_t(jump), unsafe.Pointer(&nonce[0]), unsafe.Pointer(&hash[0]), unsafe.Pointer(&sol[0]))
	return hash, bool(cFound), sol
}

func (vm *RxVM) Search(in []byte, target uint64, maxTimes uint64, jump uint32, nonce []byte) (hash []byte, found bool, sol []byte) {
	hash, found, sol = Search(vm.vm, in, target, maxTimes, jump, nonce)
	return
}
