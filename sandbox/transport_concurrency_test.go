package sandbox

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestDataPlaneTransportConcurrentStreams(t *testing.T) {
	payload := bytes.Repeat([]byte{42}, 256*1024)
	server := h2cTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client := newDataPlaneHTTPClient(server.URL)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
			if err != nil {
				t.Error(err)
				return
			}
			response, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(data, payload) {
				t.Errorf("incorrect stream payload: %d bytes", len(data))
			}
		}()
	}
	workers.Wait()
}
