package randomx

func NewRxVM(rxDataset *RxDataset, flags ...Flag) (*RxVM, error) {
	if rxDataset.rxCache == nil {
		vm, err := CreateVM(nil, rxDataset.dataset, flags...)
		return &RxVM{
			vm:        vm,
			rxDataset: nil,
		}, err
	}
	vm, err := CreateVM(rxDataset.rxCache.cache, rxDataset.dataset, flags...)
	return &RxVM{
		vm:        vm,
		rxDataset: nil,
	}, err
}

func (vm *RxVM) Close() {
	if vm.vm != nil {
		DestroyVM(vm.vm)
	}
}

func (vm *RxVM) CalcHash(in []byte) []byte {
	return CalculateHash(vm.vm, in)
}

func (vm *RxVM) CalcHashFirst(in []byte) {
	CalculateHashFirst(vm.vm, in)
}

func (vm *RxVM) CalcHashNext(in []byte) []byte {
	return CalculateHashNext(vm.vm, in)
}

func (vm *RxVM) UpdateDataset(rxDataset *RxDataset) {
	SetVMCache(vm.vm, rxDataset.rxCache.cache)
	SetVMDataset(vm.vm, rxDataset.dataset)
}
