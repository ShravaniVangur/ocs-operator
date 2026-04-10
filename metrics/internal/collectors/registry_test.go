package collectors

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCephReady(t *testing.T) {
	cephScannersExpected.Store(0)
	cephScannersReady.Store(0)

	if CephReady() {
		t.Error("CephReady() should be false when no scanners registered")
	}

	cephScannersExpected.Store(2)
	if CephReady() {
		t.Error("CephReady() should be false when ready < expected")
	}

	cephScannersReady.Store(1)
	if CephReady() {
		t.Error("CephReady() should be false when ready(1) < expected(2)")
	}

	cephScannersReady.Store(2)
	if !CephReady() {
		t.Error("CephReady() should be true when ready == expected")
	}

	cephScannersReady.Store(3)
	if !CephReady() {
		t.Error("CephReady() should be true when ready > expected")
	}
}

func TestRegisterCephScanner(t *testing.T) {
	cephScannersExpected.Store(0)
	registerCephScanner()
	registerCephScanner()
	if got := cephScannersExpected.Load(); got != 2 {
		t.Errorf("expected 2 scanners, got %d", got)
	}
}

func TestConsumerOwnerName(t *testing.T) {
	tests := []struct {
		name string
		refs []metav1.OwnerReference
		want string
	}{
		{
			name: "no refs",
			refs: nil,
			want: "",
		},
		{
			name: "non-consumer ref",
			refs: []metav1.OwnerReference{{Kind: "CephBlockPool", Name: "pool1"}},
			want: "",
		},
		{
			name: "consumer ref",
			refs: []metav1.OwnerReference{
				{Kind: "CephBlockPool", Name: "pool1"},
				{Kind: "StorageConsumer", Name: "consumer-a"},
			},
			want: "consumer-a",
		},
		{
			name: "first consumer wins",
			refs: []metav1.OwnerReference{
				{Kind: "StorageConsumer", Name: "first"},
				{Kind: "StorageConsumer", Name: "second"},
			},
			want: "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := consumerOwnerName(tt.refs); got != tt.want {
				t.Errorf("consumerOwnerName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunScanLoop(t *testing.T) {
	cephScannersReady.Store(0)
	scanWg = sync.WaitGroup{}

	stopCh := make(chan struct{})
	var callCount atomic.Int32

	scan := func() bool {
		callCount.Add(1)
		return true
	}

	runScanLoop(stopCh, 50*time.Millisecond, scan)

	// Wait for initial scan + at least one ticker fire.
	time.Sleep(150 * time.Millisecond)
	close(stopCh)
	scanWg.Wait()

	if got := callCount.Load(); got < 2 {
		t.Errorf("expected scan called at least 2 times, got %d", got)
	}
	if got := cephScannersReady.Load(); got != 1 {
		t.Errorf("expected cephScannersReady=1, got %d", got)
	}
}

func TestRunScanLoopRetry(t *testing.T) {
	cephScannersReady.Store(0)
	scanWg = sync.WaitGroup{}

	origRetry := scanRetryInterval
	scanRetryInterval = 10 * time.Millisecond
	defer func() { scanRetryInterval = origRetry }()

	stopCh := make(chan struct{})
	var callCount atomic.Int32

	scan := func() bool {
		n := callCount.Add(1)
		return n >= 3
	}

	runScanLoop(stopCh, time.Hour, scan)

	deadline := time.After(5 * time.Second)
	for cephScannersReady.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for scan to succeed")
		case <-time.After(5 * time.Millisecond):
		}
	}

	close(stopCh)
	scanWg.Wait()

	if got := callCount.Load(); got < 3 {
		t.Errorf("expected at least 3 scan calls, got %d", got)
	}
}

func TestRunScanLoopStopDuringRetry(t *testing.T) {
	cephScannersReady.Store(0)
	scanWg = sync.WaitGroup{}

	origRetry := scanRetryInterval
	scanRetryInterval = time.Hour
	defer func() { scanRetryInterval = origRetry }()

	stopCh := make(chan struct{})
	scan := func() bool { return false }

	runScanLoop(stopCh, time.Hour, scan)

	// Give the goroutine time to enter the retry wait.
	time.Sleep(50 * time.Millisecond)
	close(stopCh)
	scanWg.Wait()

	if got := cephScannersReady.Load(); got != 0 {
		t.Errorf("expected cephScannersReady=0 after stop, got %d", got)
	}
}
