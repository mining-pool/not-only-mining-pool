package vardiff

type RingBuffer struct {
	IsFull  bool
	MaxSize int64
	Cursor  int64
	Data    []int64
}

func NewRingBuffer(maxSize int64) *RingBuffer {
	return &RingBuffer{
		IsFull:  false,
		MaxSize: maxSize,
		Cursor:  0,
		Data:    make([]int64, 0),
	}
}

func (rb *RingBuffer) Append(x int64) {
	if rb.IsFull {
		rb.Data[rb.Cursor] = x
		rb.Cursor = (rb.Cursor + 1) % rb.MaxSize
	} else {
		rb.Data = append(rb.Data, x)
		rb.Cursor++
		if int64(len(rb.Data)) == rb.MaxSize {
			rb.Cursor = 0
			rb.IsFull = true
		}
	}
}

func (rb *RingBuffer) Avg() float64 {
	var sum int64
	for i := range rb.Data {
		sum = sum + rb.Data[i]
	}

	return float64(sum) / float64(rb.Size())
}

func (rb *RingBuffer) Size() int64 {
	if rb.IsFull {
		return rb.MaxSize
	} else {
		return rb.Cursor
	}
}

func (rb *RingBuffer) Clear() {
	rb.Data = make([]int64, 0)
	rb.Cursor = 0
	rb.IsFull = false
}
