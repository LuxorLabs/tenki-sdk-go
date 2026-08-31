package sandbox

import (
	"testing"
	"time"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSessionMetricsFromProtoPreservesAbsentAverages(t *testing.T) {
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	metrics := sessionMetricsFromProto(&sandboxv1.GetSessionMetricsResponse{
		SessionId:       "session-1",
		RequestedWindow: durationpb.New(5 * time.Minute),
		WindowStart:     timestamppb.New(start),
		Cpu: &sandboxv1.SessionCPUUsageAverage{
			LimitCores:       2,
			SampleCount:      3,
			ObservedDuration: durationpb.New(time.Minute),
		},
	})

	if metrics.CPU.AverageCores != nil || metrics.CPU.AveragePercent != nil {
		t.Fatal("CPU averages must remain absent")
	}
	if metrics.RequestedWindow != 5*time.Minute || metrics.WindowStart == nil || !metrics.WindowStart.Equal(start) {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	if metrics.CPU.ObservedDuration != time.Minute {
		t.Fatalf("unexpected observed duration: %s", metrics.CPU.ObservedDuration)
	}
}
