package sandbox

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestStreamOnlyKeepsChunksAndOmitsResultOutput(t *testing.T) {
	for _, discard := range []bool{false, true} {
		t.Run(map[bool]string{false: "collect", true: "discard"}[discard], func(t *testing.T) {
			_, endpoint := newDataPlaneRunTestServer(t)
			client := newStreamTestClient(t, &streamTestHandler{})
			session := newStreamTestSession(client, endpoint)
			t.Cleanup(session.Release)
			h, err := session.Command([]string{"test"}, RunOptions{StreamOnly: discard}).Stream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_ = h.Stdin.Close()
			var stdout, stderr []byte
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); stdout, _ = io.ReadAll(h.Stdout) }()
			go func() { defer wg.Done(); stderr, _ = io.ReadAll(h.Stderr) }()
			result, err := h.Wait()
			wg.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if string(stdout) != "out" || string(stderr) != "err" {
				t.Fatalf("streamed %q, %q", stdout, stderr)
			}
			if result.Status != CommandStatusSucceeded || result.Duration != 12*time.Millisecond {
				t.Fatalf("metadata: %+v", result)
			}
			if discard {
				if len(result.Stdout) != 0 || len(result.Stderr) != 0 {
					t.Fatal("discarded output retained")
				}
			} else if string(result.Stdout) != "out" || string(result.Stderr) != "err" {
				t.Fatal("collected output missing")
			}
		})
	}
}

func TestReleaseStopsRenewalWithoutTerminating(t *testing.T) {
	_, endpoint := newReproNode(t, "A", nil)
	engine := &reproEngine{sessionID: "session-repro"}
	client := newReproClient(t, engine)
	session := newReproSession(client, endpoint, "cred-initial", time.Hour)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); session.Release() }()
	}
	wg.Wait()
	if engine.terminateCalls.Load() != 0 {
		t.Fatal("release terminated sandbox")
	}
	session.dataPlaneMu.RLock()
	retained := session.renewalCancel != nil || session.dataPlaneClient != nil || session.dataPlaneCredential.reveal() != "" || len(session.renewalDones) != 0
	session.dataPlaneMu.RUnlock()
	if retained {
		t.Fatal("released handle retained local resources")
	}
	if _, err := session.Command([]string{"true"}).Stream(t.Context()); err == nil {
		t.Fatal("released handle accepted command")
	}
	if engine.credentialCalls.Load() != 0 {
		t.Fatal("released handle minted credentials")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.terminateCalls.Load() != 1 {
		t.Fatal("explicit close did not terminate")
	}
}

func TestReleaseRejectsLateCredentialResponse(t *testing.T) {
	_, endpoint := newReproNode(t, "A", nil)
	entered, proceed := make(chan struct{}), make(chan struct{})
	engine := &reproEngine{
		sessionID:        "session-repro",
		grants:           []credentialGrant{{endpoint: endpoint, credential: "late", expiresIn: time.Hour}},
		beforeCredential: func(int) { close(entered); <-proceed },
	}
	session := newReproSession(newReproClient(t, engine), endpoint, "initial", time.Hour)
	done := make(chan struct{})
	go func() { defer close(done); _ = session.renewDataPlaneCredential(t.Context()) }()
	<-entered
	session.Release()
	close(proceed)
	<-done
	session.dataPlaneMu.RLock()
	retained := session.renewalCancel != nil || session.dataPlaneClient != nil || session.dataPlaneCredential.reveal() != ""
	session.dataPlaneMu.RUnlock()
	if retained {
		t.Fatal("late credential rearmed released handle")
	}
}

func TestFailedCloseKeepsReleasedHandleClosed(t *testing.T) {
	_, endpoint := newReproNode(t, "A", nil)
	engine := &reproEngine{sessionID: "session-repro"}
	session := newReproSession(newReproClient(t, engine), endpoint, "initial", time.Hour)
	session.Release()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := session.Close(canceled); err == nil {
		t.Fatal("canceled close succeeded")
	}
	if _, err := session.Command([]string{"true"}).Stream(t.Context()); err == nil {
		t.Fatal("failed close reopened released handle")
	}
	if engine.credentialCalls.Load() != 0 {
		t.Fatal("released handle minted credentials")
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
