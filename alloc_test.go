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

	bumpTests = []struct {
		name      string
		chunkSize uintptr
	}{
		{"100B", 100}, {"1000B", 1000}, {"1KB", 1000}, {"10KB", 10000}, {"100KB", 100000},
		{"1MB", 1000000}, {"10MB", 10000000}, {"100MB", 100000000},
	}
)

const (
	personSize = unsafe.Sizeof(people)
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

func testBump(t *testing.T, bump allocator.BumpAllocator) {
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

func BenchmarkBuiltinGoAlloc(b *testing.B) {
	p := &people.partner

	for n := 0; n < b.N; n++ {
		*p = new(person)
		p = &(*p).partner
	}
}
