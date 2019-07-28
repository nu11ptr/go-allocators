package allocator_test

import (
	"reflect"
	"sync"
	"testing"
	"unsafe"

	allocator "github.com/nu11ptr/go-allocators"
)

var (
	people person
	ptr    unsafe.Pointer

	recycleProb = 5

	bumpTests = []struct {
		name        string
		chunkSize   uintptr
		recycleProb int
	}{
		{"100B", 100, recycleProb}, {"1KB", 1000, recycleProb}, {"10KB", 10000, recycleProb},
		{"100KB", 100000, recycleProb}, {"1MB", 1000000, recycleProb}, {"10MB", 10000000, recycleProb},
		{"100MB", 100000000, recycleProb},
	}
)

const (
	personSize = unsafe.Sizeof(people)
	ptrSize    = unsafe.Sizeof(ptr)
)

// person is just a bogus struct for testing. Goal was to squeeze in many
// different types to ensure a truly zero'd struct is equiv to a zero'd struct
// from the Go allocator
type person struct {
	name, address string
	female        bool
	weight        float64
	age           int
	partner       *person
	children      []person
	childMap      map[string]person
}

func TestBump(t *testing.T) {
	bump := allocator.NewBump(personSize + 10)
	testBump(t, bump)
}

func TestFastBump(t *testing.T) {
	bump := allocator.NewFastBump(personSize + 10)
	testBump(t, bump)
}

func testBump(t *testing.T, bump allocator.BasicBump) {
	t.Run("Alloc", func(t *testing.T) {
		p := (*person)(bump.Alloc(personSize))
		if !reflect.DeepEqual(p, new(person)) {
			t.Error("Allocation was not zeroed")
		}
	})
	t.Run("FreeSpace", func(t *testing.T) {
		if bump.FreeSpace() != 10 {
			t.Errorf("Wrong amount of free space (%d)", bump.FreeSpace())
		}
	})
	t.Run("TooBig", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Panic expected")
			}
		}()

		bump.Alloc(personSize + 11)
	})
}

func TestBumpThreads(t *testing.T) {
	bump := allocator.NewBump(personSize + 10)
	times := 100
	var wg sync.WaitGroup

	for i := 0; i < times; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < times; j++ {
				p := (*person)(bump.Alloc(personSize))
				if !reflect.DeepEqual(p, new(person)) {
					t.Error("Allocation was not zeroed")
				}
			}
		}()
	}

	wg.Wait()
}

func TestBumpRecycle(t *testing.T) {
	_, err := allocator.NewBumpRecycle(10, 9)
	if err == nil {
		t.Error("No error received, but error expected")
	}
	bump, err := allocator.NewBumpRecycle(personSize, ((personSize+ptrSize)*3)+10)
	if err != nil {
		t.Error("Error creating bump recycler")
	}
	testBumpRecycle(t, bump)
}

func TestFastBumpRecycle(t *testing.T) {
	_, err := allocator.NewFastBumpRecycle(10, 9)
	if err == nil {
		t.Error("No error received, but error expected")
	}
	bump, err := allocator.NewFastBumpRecycle(personSize, ((personSize+ptrSize)*3)+10)
	if err != nil {
		t.Error("Error creating bump recycler")
	}
	testBumpRecycle(t, bump)
}

func testBumpRecycle(t *testing.T, bump allocator.RecyclingBump) {
	t.Run("Alloc/Recycle", func(t *testing.T) {
		p := (*person)(bump.Alloc())
		if !reflect.DeepEqual(p, new(person)) {
			t.Error("Allocation was not zeroed")
		}
		p.name = "Teddy"

		p2 := (*person)(bump.Alloc())
		if !reflect.DeepEqual(p2, new(person)) {
			t.Error("Allocation was not zeroed")
		}
		p2.name = "Tucker"

		bump.Recycle(unsafe.Pointer(p))
		bump.Recycle(unsafe.Pointer(p2))
	})
	t.Run("AllocWithMostlyRecycled", func(t *testing.T) {
		// We didn't clear our objects before returning so they should still match
		p := (*person)(bump.Alloc())
		if !reflect.DeepEqual(p, &person{name: "Tucker"}) {
			t.Error("Allocation didn't match")
		}
		p = (*person)(bump.Alloc())
		if !reflect.DeepEqual(p, &person{name: "Teddy"}) {
			t.Error("Allocation didn't match")
		}
		// Out of recycled objects, this one should be zeroed
		p = (*person)(bump.Alloc())
		if !reflect.DeepEqual(p, new(person)) {
			t.Error("Allocation was not zeroed")
		}
	})
	t.Run("FreeSpace", func(t *testing.T) {
		if bump.FreeSpace() != (10 + ptrSize) {
			t.Errorf("Wrong amount of free space (%d)", bump.FreeSpace())
		}
	})
}

func TestBumpRecycledThreads(t *testing.T) {
	bump, _ := allocator.NewBumpRecycle(personSize, personSize+10)
	times := 100
	var wg sync.WaitGroup

	for i := 0; i < times; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < times; j++ {
				p := (*person)(bump.Alloc())
				if !reflect.DeepEqual(p, new(person)) {
					t.Error("Allocation was not zeroed")
				}
				if j%recycleProb == 0 {
					bump.Recycle(unsafe.Pointer(p))
				}
			}
		}()
	}

	wg.Wait()
}

// *** Benchmarks ***

func BenchmarkBump(b *testing.B) {
	for _, test := range bumpTests {
		b.Run(test.name, func(b *testing.B) {
			bump := allocator.NewBump(test.chunkSize)
			p := &people.partner

			for n := 0; n < b.N; n++ {
				*p = (*person)(bump.Alloc(personSize))
				p = &(*p).partner
			}
		})
	}
}

func BenchmarkFastBump(b *testing.B) {
	for _, test := range bumpTests {
		b.Run(test.name, func(b *testing.B) {
			bump := allocator.NewFastBump(test.chunkSize)
			p := &people.partner

			for n := 0; n < b.N; n++ {
				*p = (*person)(bump.Alloc(personSize))
				p = &(*p).partner
			}
		})
	}
}

func BenchmarkBumpRecycle(b *testing.B) {
	for _, test := range bumpTests {
		b.Run(test.name, func(b *testing.B) {
			bump, _ := allocator.NewBumpRecycle(personSize, test.chunkSize)
			p := &people.partner

			for n := 0; n < b.N; n++ {
				*p = (*person)(bump.Alloc())
				if n%test.recycleProb == 0 {
					bump.Recycle(unsafe.Pointer(*p))
					continue
				}
				p = &(*p).partner
			}
		})
	}
}

func BenchmarkFastBumpRecycle(b *testing.B) {
	for _, test := range bumpTests {
		b.Run(test.name, func(b *testing.B) {
			bump, _ := allocator.NewFastBumpRecycle(personSize, test.chunkSize)
			p := &people.partner

			for n := 0; n < b.N; n++ {
				*p = (*person)(bump.Alloc())
				if n%test.recycleProb == 0 {
					bump.Recycle(unsafe.Pointer(*p))
					continue
				}
				p = &(*p).partner
			}
		})
	}
}

func BenchmarkBuiltinGoAlloc(b *testing.B) {
	p := &people.partner

	for n := 0; n < b.N; n++ {
		*p = new(person)
		p = &(*p).partner
	}
}
