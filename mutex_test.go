// Most tests are adapted from https://golang.org/src/sync/mutex_test.go
// GOMAXPROCS=10 go test

package csync

import (
	"log"
	"runtime"
	"testing"
	"time"

	"golang.org/x/net/context"
)
import "sync/atomic"

func TestMutexPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("unlock of unlocked mutex did not panic")
		}
	}()

	var mu Mutex
	mu.Lock()
	mu.Unlock()
	mu.Unlock()
}

func HammerMutex(m *Mutex, loops int, cdone chan bool) {
	for i := 0; i < loops; i++ {
		m.Lock()
		m.Unlock()
	}
	cdone <- true
}

func HammerCMutex(ctx context.Context, m *Mutex, loops int, cdone chan bool, canc *int32) {
	defer func() {
		cdone <- true
	}()
	for i := 0; i < loops; i++ {
		if err := m.CLock(ctx); err != nil {
			atomic.AddInt32(canc, 1)
			return
		}
		m.Unlock()
	}
}

func TestCMutex(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	m := &Mutex{}
	c := make(chan bool)
	ctx, cf := context.WithCancel(context.Background())
	var nc int32
	for i := 0; i < 200; i++ {
		go HammerCMutex(ctx, m, 10000, c, &nc)
	}
	for i := 0; i < 200; i++ {
		<-c
		cf()
	}
	if m.c != 0 {
		t.Fatal("mutex is not in unlocked state in the end")
	}
	if runtime.GOMAXPROCS(0) != 1 && nc == 0 {
		t.Fatal("Expected some cancelations, got 0")
	}
}

func TestMutex(t *testing.T) {
	m := &Mutex{}
	c := make(chan bool)
	for i := 0; i < 10; i++ {
		go HammerMutex(m, 1000, c)
	}
	for i := 0; i < 10; i++ {
		<-c
	}
}

func BenchmarkMutexUncontended(b *testing.B) {
	type PaddedMutex struct {
		Mutex
		pad [128]uint8
	}
	b.RunParallel(func(pb *testing.PB) {
		var mu PaddedMutex
		for pb.Next() {
			mu.Lock()
			mu.Unlock()
		}
	})
}

func benchmarkMutex(b *testing.B, slack, work bool) {
	var mu Mutex
	if slack {
		b.SetParallelism(10)
	}
	b.RunParallel(func(pb *testing.PB) {
		foo := 0
		for pb.Next() {
			mu.Lock()
			mu.Unlock()
			if work {
				for i := 0; i < 100; i++ {
					foo *= 2
					foo /= 2
				}
			}
		}
		_ = foo
	})
}

func BenchmarkMutex(b *testing.B) {
	benchmarkMutex(b, false, false)
}

func BenchmarkMutexSlack(b *testing.B) {
	benchmarkMutex(b, true, false)
}

func BenchmarkMutexWork(b *testing.B) {
	benchmarkMutex(b, false, true)
}

func BenchmarkMutexWorkSlack(b *testing.B) {
	benchmarkMutex(b, true, true)
}

func BenchmarkMutexNoSpin(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// non-profitable and allows to confirm that spinning does not do harm.
	// To achieve this we create excess of goroutines most of which do local work.
	// These goroutines yield during local work, so that switching from
	// a blocked goroutine to other goroutines is profitable.
	// As a matter of fact, this benchmark still triggers some spinning in the mutex.
	var m Mutex
	var acc0, acc1 uint64
	b.SetParallelism(4)
	b.RunParallel(func(pb *testing.PB) {
		c := make(chan bool)
		var data [4 << 10]uint64
		for i := 0; pb.Next(); i++ {
			if i%4 == 0 {
				m.Lock()
				acc0 -= 100
				acc1 += 100
				m.Unlock()
			} else {
				for i := 0; i < len(data); i += 4 {
					data[i]++
				}
				// Elaborate way to say runtime.Gosched
				// that does not put the goroutine onto global runq.
				go func() {
					c <- true
				}()
				<-c
			}
		}
	})
}

func BenchmarkMutexSpin(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// profitable. To achieve this we create a goroutine per-proc.
	// These goroutines access considerable amount of local data so that
	// unnecessary rescheduling is penalized by cache misses.
	var m Mutex
	var acc0, acc1 uint64
	b.RunParallel(func(pb *testing.PB) {
		var data [16 << 10]uint64
		for i := 0; pb.Next(); i++ {
			m.Lock()
			acc0 -= 100
			acc1 += 100
			m.Unlock()
			for i := 0; i < len(data); i += 4 {
				data[i]++
			}
		}
	})
}

func BenchmarkCMutexUncontended(b *testing.B) {
	ctx := context.TODO()
	type PaddedMutex struct {
		Mutex
		pad [128]uint8
	}
	b.RunParallel(func(pb *testing.PB) {
		var mu PaddedMutex
		for pb.Next() {
			if mu.CLock(ctx) != nil {
				return
			}
			mu.Unlock()
		}
	})
}

func BenchmarkCMutexUncontendedTimeout(b *testing.B) {
	ctx, cf := context.WithTimeout(context.TODO(), time.Millisecond)
	defer cf()
	type PaddedMutex struct {
		Mutex
		pad [128]uint8
	}
	b.RunParallel(func(pb *testing.PB) {
		var mu PaddedMutex
		for pb.Next() {
			if mu.CLock(ctx) != nil {
				return
			}
			mu.Unlock()
		}
	})
}

func benchmarkCMutex(ctx context.Context, b *testing.B, slack, work bool) {
	var mu Mutex
	if slack {
		b.SetParallelism(10)
	}
	b.RunParallel(func(pb *testing.PB) {
		foo := 0
		for pb.Next() {
			if mu.CLock(ctx) != nil {
				return
			}
			mu.Unlock()
			if work {
				for i := 0; i < 100; i++ {
					foo *= 2
					foo /= 2
				}
			}
		}
		_ = foo
	})
}

func BenchmarkCMutex(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, false, false)
}

func BenchmarkCMutexSlack(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, true, false)
}

func BenchmarkCMutexWork(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, false, true)
}

func BenchmarkCMutexWorkSlack(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, true, true)
}

func BenchmarkCMutexNoSpin(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// non-profitable and allows to confirm that spinning does not do harm.
	// To achieve this we create excess of goroutines most of which do local work.
	// These goroutines yield during local work, so that switching from
	// a blocked goroutine to other goroutines is profitable.
	// As a matter of fact, this benchmark still triggers some spinning in the mutex.
	var m Mutex
	ctx := context.TODO()
	var acc0, acc1 uint64
	b.SetParallelism(4)
	b.RunParallel(func(pb *testing.PB) {
		c := make(chan bool)
		var data [4 << 10]uint64
		for i := 0; pb.Next(); i++ {
			if i%4 == 0 {
				if m.CLock(ctx) != nil {
					return
				}
				acc0 -= 100
				acc1 += 100
				m.Unlock()
			} else {
				for i := 0; i < len(data); i += 4 {
					data[i]++
				}
				// Elaborate way to say runtime.Gosched
				// that does not put the goroutine onto global runq.
				go func() {
					c <- true
				}()
				<-c
			}
		}
	})
}

func BenchmarkCMutexSpin(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// profitable. To achieve this we create a goroutine per-proc.
	// These goroutines access considerable amount of local data so that
	// unnecessary rescheduling is penalized by cache misses.
	var m Mutex
	ctx := context.TODO()
	var acc0, acc1 uint64
	b.RunParallel(func(pb *testing.PB) {
		var data [16 << 10]uint64
		for i := 0; pb.Next(); i++ {
			if m.CLock(ctx) != nil {
				return
			}
			acc0 -= 100
			acc1 += 100
			m.Unlock()
			for i := 0; i < len(data); i += 4 {
				data[i]++
			}
		}
	})
}

func BenchmarkCMutexTimeout(b *testing.B) {
	ctx, cf := context.WithTimeout(context.TODO(), time.Millisecond)
	defer cf()
	benchmarkCMutex(ctx, b, false, false)
}

func BenchmarkCMutexSlackTimeout(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, true, false)
}

func BenchmarkCMutexWorkTimeout(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, false, true)
}

func BenchmarkCMutexWorkSlackTimeout(b *testing.B) {
	benchmarkCMutex(context.TODO(), b, true, true)
}

func BenchmarkCMutexNoSpinTimeout(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// non-profitable and allows to confirm that spinning does not do harm.
	// To achieve this we create excess of goroutines most of which do local work.
	// These goroutines yield during local work, so that switching from
	// a blocked goroutine to other goroutines is profitable.
	// As a matter of fact, this benchmark still triggers some spinning in the mutex.
	var m Mutex
	ctx := context.TODO()
	var acc0, acc1 uint64
	b.SetParallelism(4)
	b.RunParallel(func(pb *testing.PB) {
		c := make(chan bool)
		var data [4 << 10]uint64
		for i := 0; pb.Next(); i++ {
			if i%4 == 0 {
				if m.CLock(ctx) != nil {
					return
				}
				acc0 -= 100
				acc1 += 100
				m.Unlock()
			} else {
				for i := 0; i < len(data); i += 4 {
					data[i]++
				}
				// Elaborate way to say runtime.Gosched
				// that does not put the goroutine onto global runq.
				go func() {
					c <- true
				}()
				<-c
			}
		}
	})
}

func BenchmarkCMutexSpinTimeout(b *testing.B) {
	// This benchmark models a situation where spinning in the mutex should be
	// profitable. To achieve this we create a goroutine per-proc.
	// These goroutines access considerable amount of local data so that
	// unnecessary rescheduling is penalized by cache misses.
	var m Mutex
	ctx := context.TODO()
	var acc0, acc1 uint64
	b.RunParallel(func(pb *testing.PB) {
		var data [16 << 10]uint64
		for i := 0; pb.Next(); i++ {
			if m.CLock(ctx) != nil {
				return
			}
			acc0 -= 100
			acc1 += 100
			m.Unlock()
			for i := 0; i < len(data); i += 4 {
				data[i]++
			}
		}
	})
}
