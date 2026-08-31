package sandbox

import (
	"context"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SessionCPUUsageAverage struct {
	AverageCores     *float64
	AveragePercent   *float64
	LimitCores       float64
	SampleCount      uint64
	ObservedDuration time.Duration
	CoveragePercent  float64
	FirstSampleAt    *time.Time
	LastSampleAt     *time.Time
}

type SessionMemoryUsageAverage struct {
	AverageBytes     *float64
	AveragePercent   *float64
	LimitBytes       uint64
	SampleCount      uint64
	ObservedDuration time.Duration
	CoveragePercent  float64
	FirstSampleAt    *time.Time
	LastSampleAt     *time.Time
}

type SessionMetrics struct {
	SessionID       string
	RequestedWindow time.Duration
	WindowStart     *time.Time
	WindowEnd       *time.Time
	CPU             SessionCPUUsageAverage
	Memory          SessionMemoryUsageAverage
}

func (c *Client) GetSessionMetrics(ctx context.Context, sessionID string, window time.Duration) (*SessionMetrics, error) {
	req := &sandboxv1.GetSessionMetricsRequest{SessionId: sessionID}
	if window != 0 {
		req.Window = durationpb.New(window)
	}
	resp, err := c.sandbox.GetSessionMetrics(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return sessionMetricsFromProto(resp.Msg), nil
}

func (s *Session) Metrics(ctx context.Context, window time.Duration) (*SessionMetrics, error) {
	return s.client.GetSessionMetrics(ctx, s.ID, window)
}

func sessionMetricsFromProto(metrics *sandboxv1.GetSessionMetricsResponse) *SessionMetrics {
	if metrics == nil {
		return &SessionMetrics{}
	}
	return &SessionMetrics{
		SessionID:       metrics.GetSessionId(),
		RequestedWindow: durationFromProto(metrics.GetRequestedWindow()),
		WindowStart:     timeFromProto(metrics.GetWindowStart()),
		WindowEnd:       timeFromProto(metrics.GetWindowEnd()),
		CPU:             cpuUsageAverageFromProto(metrics.GetCpu()),
		Memory:          memoryUsageAverageFromProto(metrics.GetMemory()),
	}
}

func cpuUsageAverageFromProto(usage *sandboxv1.SessionCPUUsageAverage) SessionCPUUsageAverage {
	if usage == nil {
		return SessionCPUUsageAverage{}
	}
	return SessionCPUUsageAverage{
		AverageCores:     usage.AverageCores,
		AveragePercent:   usage.AveragePercent,
		LimitCores:       usage.GetLimitCores(),
		SampleCount:      usage.GetSampleCount(),
		ObservedDuration: durationFromProto(usage.GetObservedDuration()),
		CoveragePercent:  usage.GetCoveragePercent(),
		FirstSampleAt:    timeFromProto(usage.GetFirstSampleAt()),
		LastSampleAt:     timeFromProto(usage.GetLastSampleAt()),
	}
}

func memoryUsageAverageFromProto(usage *sandboxv1.SessionMemoryUsageAverage) SessionMemoryUsageAverage {
	if usage == nil {
		return SessionMemoryUsageAverage{}
	}
	return SessionMemoryUsageAverage{
		AverageBytes:     usage.AverageBytes,
		AveragePercent:   usage.AveragePercent,
		LimitBytes:       usage.GetLimitBytes(),
		SampleCount:      usage.GetSampleCount(),
		ObservedDuration: durationFromProto(usage.GetObservedDuration()),
		CoveragePercent:  usage.GetCoveragePercent(),
		FirstSampleAt:    timeFromProto(usage.GetFirstSampleAt()),
		LastSampleAt:     timeFromProto(usage.GetLastSampleAt()),
	}
}

func durationFromProto(value *durationpb.Duration) time.Duration {
	if value == nil {
		return 0
	}
	return value.AsDuration()
}

func timeFromProto(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.AsTime()
	return &converted
}
