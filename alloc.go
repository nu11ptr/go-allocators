package allocator

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// BasicBump is an allocator that simply bumps a pointer in a larger memory region and returns
// it as the next object. If there isn't enough memory in the region, another one is allocated.
type BasicBump interface {
	Alloc(size uintptr) unsafe.Pointer
	FreeSpace() uintptr
}

// RecyclingBump is an allocator that tries to use recycled objects before bump allocating. If a
// bump allocation is needed, and there isn't enough memory in the region, another one is allocated.
type RecyclingBump interface {
	Alloc() unsafe.Pointer
	Recycle(unsafe.Pointer)
	FreeSpace() uintptr
}

// *** Thread-safe Bump Allocator ***

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

// *** Fast bump allocator (not thread-safe) ***

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

// *** Thread-safe bump allocator with recycling ***

// BumpRecycle allocator is a thread safe bump allocator with object recycling. The allocator can be used
// for a single object type or multiple, however, the largest possible object must be specified as 'allocSize'.
// The allocator does not allocate any memory until the first request. Objects can be recycled by
// returning them, they are not cleared (if that is desired, the caller must do it). Memory is not
// eligible for garbage collection until all objects in a 'region' are not in use and the next region
// has been requested.
type BumpRecycle struct {
	recycled                       unsafe.Pointer
	allocSize, regionSize, freePtr uintptr
	region                         []byte
	lock                           sync.Mutex
}

var ptr unsafe.Pointer

const ptrSize = unsafe.Sizeof(ptr)

// NewBumpRecycle returns a new bump allocator with recycling. 'allocSize' should be equal to the
// largest object to be requested. 'regionSize' determines the size of each memory region. An error
// is only returned if allocSize is greater than regionSize
func NewBumpRecycle(allocSize, regionSize uintptr) (*BumpRecycle, error) {
	if allocSize > regionSize {
		return nil, errors.New("Allocation size must be less than or equal to region size")
	}
	// We add an extra pointer to front of every allocation so we can create a linked list of recycled objects
	return &BumpRecycle{
		allocSize:  allocSize + ptrSize,
		regionSize: regionSize + ptrSize, // ptrSize only added here to ensure regionSize >= allocSize
		freePtr:    regionSize,
	}, nil
}

// Alloc allocates memory of the size requested by first checking and using a 'recycle' list and if empty
// 'bumping' the freePtr to the next position to get an allocation. If there isn't enough memory left
// in the region, a new memory region is first allocated
func (b *BumpRecycle) Alloc() unsafe.Pointer {
	b.lock.Lock()

	// If we have recycled objects, use them first
	if b.recycled != nil {
		// Get allocation from front of linked list (stored just past the 'next' pointer)
		obj := unsafe.Pointer(uintptr(b.recycled) + ptrSize)
		// The first field in the allocation is actually an unsafe.Pointer pointing at next free allocation
		next := (*unsafe.Pointer)(b.recycled)
		// Update the linked list to point point at the next entry (effectively popping top entry)
		b.recycled = *next
		// Nil out the value of the next pointer inside allocation now that we are handing this out
		*next = nil

		b.lock.Unlock()
		return obj
	}

	// Are we out of memory?
	if b.allocSize > b.regionSize-b.freePtr {
		// Allocate memory and zero it - intentionally dumping reference to the old region (eligible for
		// garbage collection when none of its objects are no longer alive)
		b.region = make([]byte, b.regionSize)
		b.freePtr = 0
	}

	obj := b.region[b.freePtr+ptrSize:]
	b.freePtr += b.allocSize
	b.lock.Unlock()
	return unsafe.Pointer(&obj[0])
}

// Recycle recycles an allocation for later reuse by Alloc
func (b *BumpRecycle) Recycle(obj unsafe.Pointer) {
	b.lock.Lock()

	// Save front of linked list
	prev := b.recycled
	// Set new front of linked list to one pointer width in front of allocation ('next' field)
	b.recycled = unsafe.Pointer(uintptr(obj) - ptrSize)
	// Set 'next' field to previous value
	*(*unsafe.Pointer)(b.recycled) = prev

	b.lock.Unlock()
}

// FreeSpace returns the amount of free space left in the allocator without allocating more memory from main allocator
func (b *BumpRecycle) FreeSpace() uintptr {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.regionSize - b.freePtr
}

// *** Bump allocator with recycling (not thread-safe) ***

// FastBumpRecycle allocator is a bump allocator with object recycling. It is not thread safe and is
// designed to be used in only a single go routine. The allocator can be used for a single object type
// or multiple, however, the largest possible object must be specified as 'allocSize'.The allocator
// does not allocate any memory until the first request. Objects can be recycled by returning them,
// they are not cleared (if that is desired, the caller must do it). Memory is not eligible for
// garbage collection until all objects in a 'region' are not in use and the next region has been
// requested.
type FastBumpRecycle struct {
	recycled                       unsafe.Pointer
	allocSize, regionSize, freePtr uintptr
	region                         []byte
}

// NewFastBumpRecycle returns a new bump allocator with recycling. 'allocSize' should be equal to the
// largest object to be requested. 'regionSize' determines the size of each memory region. An error
// is only returned if allocSize is greater than regionSize
func NewFastBumpRecycle(allocSize, regionSize uintptr) (*FastBumpRecycle, error) {
	if allocSize > regionSize {
		return nil, errors.New("Allocation size must be less than or equal to region size")
	}
	// We add an extra pointer to front of every allocation so we can create a linked list of recycled objects
	return &FastBumpRecycle{
		allocSize:  allocSize + ptrSize,
		regionSize: regionSize + ptrSize,
		freePtr:    regionSize,
	}, nil
}

// Alloc allocates memory of the size requested by first checking and using a 'recycle' list and if empty
// 'bumping' the freePtr to the next position to get an allocation. If there isn't enough memory left
// in the region, a new memory region is first allocated
func (b *FastBumpRecycle) Alloc() unsafe.Pointer {
	// If we have recycled objects, use them first
	if b.recycled != nil {
		// Get allocation from front of linked list (stored just past the 'next' pointer)
		obj := unsafe.Pointer(uintptr(b.recycled) + ptrSize)
		// The first field in the allocation is actually an unsafe.Pointer pointing at next free allocation
		next := (*unsafe.Pointer)(b.recycled)
		// Update the linked list to point point at the next entry (effectively popping top entry)
		b.recycled = *next
		// Nil out the value of the next pointer inside allocation now that we are handing this out
		*next = nil

		return obj
	}

	// Are we out of memory?
	if b.allocSize > b.regionSize-b.freePtr {
		// Allocate memory and zero it - intentionally dumping reference to the old region (eligible for
		// garbage collection when none of its objects are no longer alive)
		b.region = make([]byte, b.regionSize)
		b.freePtr = 0
	}

	obj := b.region[b.freePtr+ptrSize:]
	b.freePtr += b.allocSize
	return unsafe.Pointer(&obj[0])
}

// Recycle recycles an allocation for later reuse by Alloc
func (b *FastBumpRecycle) Recycle(obj unsafe.Pointer) {
	// Save front of linked list
	prev := b.recycled
	// Set new front of linked list to one pointer width in front of allocation ('next' field)
	b.recycled = unsafe.Pointer(uintptr(obj) - ptrSize)
	// Set 'next' field to previous value
	*(*unsafe.Pointer)(b.recycled) = prev
}

// FreeSpace returns the amount of free space left in the allocator without allocating more memory from main allocator
func (b *FastBumpRecycle) FreeSpace() uintptr {
	return b.regionSize - b.freePtr
}
