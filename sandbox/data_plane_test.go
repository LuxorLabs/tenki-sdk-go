package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestIsEdgeNotReady(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Edge returns a plain HTTP 404 before the per-session route applies;
		// connect maps it to Unimplemented and carries the HTTP status text.
		{"edge 404", connect.NewError(connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found")), true},
		// A genuine node-agent capability-unavailable is Unimplemented without a 404.
		{"real unimplemented", connect.NewError(connect.CodeUnimplemented, errors.New("run is not implemented")), false},
		// Other transport errors must not be treated as edge-not-ready.
		{"unavailable", connect.NewError(connect.CodeUnavailable, errors.New("connection refused")), false},
		{"not found 404 wrong code", connect.NewError(connect.CodeNotFound, errors.New("HTTP status 404")), false},
		{"unimplemented unrelated digits", connect.NewError(connect.CodeUnimplemented, errors.New("capability version 4040 missing")), false},
		{"plain error", errors.New("HTTP status 404 Not Found"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEdgeNotReady(tc.err); got != tc.want {
				t.Fatalf("isEdgeNotReady(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSharedDataPlaneHTTPClientReferenceLifecycle(t *testing.T) {
	client := &Client{}
	first, releaseFirst, err := client.acquireDataPlaneHTTPClient("https://node.test/path-a")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, releaseSecond, err := client.acquireDataPlaneHTTPClient("https://node.test/path-b")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if first != second {
		t.Fatal("same origin should share one HTTP/2 client")
	}
	if got := client.dataPlanePool["https://node.test"].references; got != 2 {
		t.Fatalf("references got %d, want 2", got)
	}
	releaseFirst()
	if got := client.dataPlanePool["https://node.test"].references; got != 1 {
		t.Fatalf("references after first release got %d, want 1", got)
	}
	releaseSecond()
	if _, ok := client.dataPlanePool["https://node.test"]; ok {
		t.Fatal("last release should remove the shared client")
	}
}

func TestDataPlaneEndpointResolutionIsSingleflight(t *testing.T) {
	hints := &dataPlaneEndpointHints{}
	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoint, err := hints.resolve(context.Background(), func() error {
				calls.Add(1)
				time.Sleep(10 * time.Millisecond)
				hints.remember("https://node.test")
				return nil
			})
			if err != nil {
				errs <- err
				return
			}
			if endpoint != "https://node.test" {
				errs <- errors.New("unexpected endpoint")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls got %d, want 1", got)
	}
}

func TestDataPlaneEndpointResolutionWaitersRetryResolverFailure(t *testing.T) {
	hints := &dataPlaneEndpointHints{}
	var calls atomic.Int32
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoint, err := hints.resolve(context.Background(), func() error {
				call := calls.Add(1)
				time.Sleep(10 * time.Millisecond)
				if call == 1 {
					return errors.New("transient resolver failure")
				}
				hints.remember("https://node.test")
				return nil
			})
			if err != nil {
				failures.Add(1)
				return
			}
			if endpoint != "https://node.test" {
				t.Errorf("endpoint = %q, want shared node hint", endpoint)
				return
			}
			successes.Add(1)
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Fatalf("resolver calls got %d, want 2", got)
	}
	if got := failures.Load(); got != 1 {
		t.Fatalf("resolver failures got %d, want 1", got)
	}
	if got := successes.Load(); got != 7 {
		t.Fatalf("successful waiters got %d, want 7", got)
	}
}

func TestClientSessionsIsolateDataPlaneEndpointHints(t *testing.T) {
	client := &Client{}
	first := newSession(client, nil)
	second := newSession(client, nil)
	first.endpointHints().remember("https://node.test")

	if got := second.endpointHints().current(); got != "" {
		t.Fatalf("second endpoint = %q, want isolated node hint", got)
	}
}

func TestDataPlaneReadyBackoff(t *testing.T) {
	if got := dataPlaneReadyBackoff(0); got != 50*time.Millisecond {
		t.Fatalf("attempt 0 = %v, want 50ms", got)
	}
	// Capped at 750ms regardless of attempt.
	if got := dataPlaneReadyBackoff(100); got != 750*time.Millisecond {
		t.Fatalf("attempt 100 = %v, want 750ms (cap)", got)
	}
	// Monotonic non-decreasing up to the cap.
	prev := time.Duration(0)
	for a := range 32 {
		d := dataPlaneReadyBackoff(a)
		if d < prev {
			t.Fatalf("backoff decreased at attempt %d: %v < %v", a, d, prev)
		}
		prev = d
	}
}

func TestDataPlaneNotReadyErrorContract(t *testing.T) {
	err := dataPlaneNotReadyError(connect.NewError(connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found")))
	if !IsDataPlaneNotReady(err) {
		t.Fatal("IsDataPlaneNotReady should match DataPlaneNotReadyError")
	}
	if IsCapabilityUnavailable(err) {
		t.Fatal("edge 404 readiness errors must not look like capability unavailable")
	}
	var retryable interface{ IsRetryable() bool }
	if !errors.As(err, &retryable) || !retryable.IsRetryable() {
		t.Fatal("DataPlaneNotReadyError should be marked retryable")
	}
}

func TestMapErrorMapsEdge404ToDataPlaneNotReady(t *testing.T) {
	err := mapError(connect.NewError(connect.CodeUnimplemented, errors.New("HTTP status 404 Not Found")))
	if !IsDataPlaneNotReady(err) {
		t.Fatalf("mapError should return DataPlaneNotReadyError, got %T: %v", err, err)
	}
}
