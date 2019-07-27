package allocator

import (
	"fmt"
	"sync"
	"unsafe"
)

// BumpAllocator is an allocator that simply bumps a pointer in a larger memory region and returns
// it as the next object. If there isn't enough memory in the region, another one is allocated.
type BumpAllocator interface {
	Alloc(size uintptr) unsafe.Pointer
	FreeSpace() uintptr
}

// Bump allocator is a thread safe bump allocator. The allocator does not allocate any memory until
// the first request. Memory is not returned or eligible for garbage collection until all objects in a
// 'region' are not in use and the next region has been requested.
type Bump struct {
	regionSize, freePtr uintptr
	region              []byte
	lock                sync.Mutex
}

// NewBump returns a new bump allocator. 'regionSize' determines the size of each memory region
func NewBump(regionSize uintptr) *Bump {
	return &Bump{regionSize: regionSize, freePtr: regionSize}
}

// Alloc allocates memory of the size requested by 'bumping' the freePtr to the next position. If there
// isn't enough memory left in the region, a new memory region is first allocated
func (b *Bump) Alloc(size uintptr) unsafe.Pointer {
	b.lock.Lock()

	// Are we out of memory?
	if size > b.regionSize-b.freePtr {
		regionSize := b.regionSize
		// If alloc request is larger than region size, panic...
		if size > regionSize {
			b.lock.Unlock()
			panic(fmt.Sprintf("Requested object size (%d) is larger than region size (%d)", size, b.regionSize))
		}
		// Allocate memory and zero it - intentionally dumping reference to the old region (eligible for
		// garbage collection when none of its objects are no longer alive)
		b.region = make([]byte, regionSize)
		b.freePtr = 0
	}

	obj := b.region[b.freePtr:]
	b.freePtr += size
	b.lock.Unlock()
	return unsafe.Pointer(&obj[0])
}

// FreeSpace returns the amount of free space left in the allocator without allocating more memory from main allocator
func (b *Bump) FreeSpace() uintptr {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.regionSize - b.freePtr
}

// FastBump allocator is not thread safe and is designed to be used in only a single go routine. The allocator
// does not allocate any memory until the first request. Memory is not returned or eligible for
// garbage collection until all objects in a 'region' are not in use and the next region has been requested.
type FastBump struct {
	regionSize, freePtr uintptr
	region              []byte
}

// NewFastBump returns a new bump allocator. 'regionSize' determines the size of each memory region
func NewFastBump(regionSize uintptr) *FastBump {
	return &FastBump{regionSize: regionSize, freePtr: regionSize}
}

// Alloc allocates memory of the size requested by 'bumping' the freePtr to the next position. If there
// isn't enough memory left in the region, a new memory region is first allocated
func (b *FastBump) Alloc(size uintptr) unsafe.Pointer {
	// Are we out of memory?
	if size > b.regionSize-b.freePtr {
		regionSize := b.regionSize
		// If alloc is larger than region size, panic
		if size > regionSize {
			panic(fmt.Sprintf("Requested object size (%d) is larger than region size (%d)", size, b.regionSize))
		}
		// Allocate memory and zero it - intentionally dumping old region (eligible for
		// garbage collection when none of its objects are stil alive)
		b.region = make([]byte, regionSize)
		b.freePtr = 0
	}

	obj := b.region[b.freePtr:]
	b.freePtr += size
	return unsafe.Pointer(&obj[0])
}

// FreeSpace returns the amount of free space left in the allocator without allocating more memory from main allocator
func (b *FastBump) FreeSpace() uintptr {
	return b.regionSize - b.freePtr
}
