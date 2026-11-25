package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
	"go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1/sandboxv1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSessionCostFromProtoMapsEveryField(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	cost := sessionCostFromProto(&sandboxv1.SessionCost{
		SessionId: "session-1",
		Status:    sandboxv1.SessionCostStatus_SESSION_COST_STATUS_FINAL,
		TotalUsd:  proto.String("0.0421"),
		Lines: []*sandboxv1.SessionCostLine{
			{
				Item:      sandboxv1.SessionCostItem_SESSION_COST_ITEM_CPU,
				Quantity:  "1200",
				Unit:      sandboxv1.SessionCostUnit_SESSION_COST_UNIT_VCPU_SECOND,
				AmountUsd: "0.03",
				StartedAt: timestamppb.New(start),
				EndedAt:   timestamppb.New(end),
			},
			{
				Item:      sandboxv1.SessionCostItem_SESSION_COST_ITEM_MEMORY,
				Quantity:  "2400.5",
				Unit:      sandboxv1.SessionCostUnit_SESSION_COST_UNIT_GIB_SECOND,
				AmountUsd: "0.01",
				StartedAt: timestamppb.New(start),
				EndedAt:   timestamppb.New(end),
			},
			{
				Item:      sandboxv1.SessionCostItem_SESSION_COST_ITEM_DISK,
				Quantity:  "600",
				Unit:      sandboxv1.SessionCostUnit_SESSION_COST_UNIT_GIB_SECOND,
				AmountUsd: "0.002",
				StartedAt: timestamppb.New(start),
				EndedAt:   timestamppb.New(end),
			},
			{
				Item:      sandboxv1.SessionCostItem_SESSION_COST_ITEM_PAUSED_STORAGE,
				Quantity:  "30",
				Unit:      sandboxv1.SessionCostUnit_SESSION_COST_UNIT_GIB_SECOND,
				AmountUsd: "0.0001",
				StartedAt: timestamppb.New(start),
				EndedAt:   timestamppb.New(end),
			},
		},
		AsOf:                 timestamppb.New(end),
		BilledRunningSeconds: 600,
		BilledPausedSeconds:  30,
	})

	total := "0.0421"
	want := &SessionCost{
		SessionID: "session-1",
		Status:    SessionCostStatusFinal,
		TotalUSD:  &total,
		Lines: []SessionCostLine{
			{Item: SessionCostItemCPU, Quantity: "1200", Unit: SessionCostUnitVCPUSecond, AmountUSD: "0.03", StartedAt: start, EndedAt: &end},
			{Item: SessionCostItemMemory, Quantity: "2400.5", Unit: SessionCostUnitGiBSecond, AmountUSD: "0.01", StartedAt: start, EndedAt: &end},
			{Item: SessionCostItemDisk, Quantity: "600", Unit: SessionCostUnitGiBSecond, AmountUSD: "0.002", StartedAt: start, EndedAt: &end},
			{Item: SessionCostItemPausedStorage, Quantity: "30", Unit: SessionCostUnitGiBSecond, AmountUSD: "0.0001", StartedAt: start, EndedAt: &end},
		},
		AsOf:                 &end,
		BilledRunningSeconds: 600,
		BilledPausedSeconds:  30,
	}
	if !reflect.DeepEqual(cost, want) {
		t.Fatalf("unexpected cost:\n got %#v\nwant %#v", cost, want)
	}
}

func TestSessionCostFromProtoPreservesUnsetOptionals(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cost := sessionCostFromProto(&sandboxv1.SessionCost{
		SessionId: "session-1",
		Status:    sandboxv1.SessionCostStatus_SESSION_COST_STATUS_ACCRUING,
		Lines: []*sandboxv1.SessionCostLine{{
			Item:      sandboxv1.SessionCostItem_SESSION_COST_ITEM_CPU,
			Unit:      sandboxv1.SessionCostUnit_SESSION_COST_UNIT_VCPU_SECOND,
			StartedAt: timestamppb.New(start),
		}},
	})

	if cost.Status != SessionCostStatusAccruing {
		t.Fatalf("status = %q", cost.Status)
	}
	if cost.TotalUSD != nil || cost.AsOf != nil {
		t.Fatalf("total and as-of must remain absent: %#v", cost)
	}
	if len(cost.Lines) != 1 || cost.Lines[0].EndedAt != nil || !cost.Lines[0].StartedAt.Equal(start) {
		t.Fatalf("unexpected lines: %#v", cost.Lines)
	}
}

func TestSessionCostFromProtoKeepsZeroTotalAndUnknownEnums(t *testing.T) {
	cost := sessionCostFromProto(&sandboxv1.SessionCost{
		Status:   sandboxv1.SessionCostStatus_SESSION_COST_STATUS_UNAVAILABLE,
		TotalUsd: proto.String("0"),
		Lines:    []*sandboxv1.SessionCostLine{{Item: 99, Unit: 99}},
	})

	if cost.Status != SessionCostStatusUnavailable {
		t.Fatalf("status = %q", cost.Status)
	}
	if cost.TotalUSD == nil || *cost.TotalUSD != "0" {
		t.Fatalf("zero total must remain set: %#v", cost.TotalUSD)
	}
	line := cost.Lines[0]
	if line.Item != SessionCostItemUnspecified || line.Unit != SessionCostUnitUnspecified || !line.StartedAt.IsZero() {
		t.Fatalf("unexpected line: %#v", line)
	}
	if got := sessionCostFromProto(nil); got.Status != SessionCostStatusUnspecified || got.TotalUSD != nil {
		t.Fatalf("unexpected nil conversion: %#v", got)
	}
}

type costEngine struct {
	sandboxv1connect.UnimplementedSandboxServiceHandler
	getRequest   *sandboxv1.GetSessionCostRequest
	batchRequest *sandboxv1.BatchGetSessionCostsRequest
}

func (e *costEngine) GetSessionCost(
	_ context.Context,
	req *connect.Request[sandboxv1.GetSessionCostRequest],
) (*connect.Response[sandboxv1.GetSessionCostResponse], error) {
	e.getRequest = req.Msg
	return connect.NewResponse(&sandboxv1.GetSessionCostResponse{Cost: &sandboxv1.SessionCost{
		SessionId: req.Msg.GetSessionId(),
		Status:    sandboxv1.SessionCostStatus_SESSION_COST_STATUS_FINAL,
		TotalUsd:  proto.String("1.25"),
	}}), nil
}

func (e *costEngine) BatchGetSessionCosts(
	_ context.Context,
	req *connect.Request[sandboxv1.BatchGetSessionCostsRequest],
) (*connect.Response[sandboxv1.BatchGetSessionCostsResponse], error) {
	e.batchRequest = req.Msg
	return connect.NewResponse(&sandboxv1.BatchGetSessionCostsResponse{
		Costs: []*sandboxv1.SessionCost{{
			SessionId: "session-1",
			Status:    sandboxv1.SessionCostStatus_SESSION_COST_STATUS_ACCRUING,
		}},
		MissingSessionIds: []string{"session-2"},
	}), nil
}

func newCostTestClient(t *testing.T, engine *costEngine) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(sandboxv1connect.NewSandboxServiceHandler(engine))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := New(
		WithAuthToken("tk_test"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestGetSessionCostAndSessionCost(t *testing.T) {
	t.Parallel()
	engine := &costEngine{}
	client := newCostTestClient(t, engine)

	cost, err := client.GetSessionCost(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetSessionCost: %v", err)
	}
	if engine.getRequest.GetSessionId() != "session-1" {
		t.Fatalf("session id = %q", engine.getRequest.GetSessionId())
	}
	if cost.SessionID != "session-1" || cost.Status != SessionCostStatusFinal || cost.TotalUSD == nil || *cost.TotalUSD != "1.25" {
		t.Fatalf("unexpected cost: %#v", cost)
	}

	session := &Session{ID: "session-3", client: client}
	if _, err := session.Cost(context.Background()); err != nil {
		t.Fatalf("Session.Cost: %v", err)
	}
	if engine.getRequest.GetSessionId() != "session-3" {
		t.Fatalf("session id = %q", engine.getRequest.GetSessionId())
	}
}

func TestBatchGetSessionCosts(t *testing.T) {
	t.Parallel()
	engine := &costEngine{}
	client := newCostTestClient(t, engine)

	batch, err := client.BatchGetSessionCosts(context.Background(), []string{" session-1", "session-2 "}, " ")
	if err != nil {
		t.Fatalf("BatchGetSessionCosts: %v", err)
	}
	if engine.batchRequest.GetWorkspaceId() != "" {
		t.Fatalf("workspace id = %q", engine.batchRequest.GetWorkspaceId())
	}
	if !reflect.DeepEqual(engine.batchRequest.GetSessionIds(), []string{"session-1", "session-2"}) {
		t.Fatalf("session ids = %v", engine.batchRequest.GetSessionIds())
	}
	if len(batch.Costs) != 1 || batch.Costs[0].SessionID != "session-1" || batch.Costs[0].TotalUSD != nil {
		t.Fatalf("unexpected costs: %#v", batch.Costs)
	}
	if !reflect.DeepEqual(batch.MissingSessionIDs, []string{"session-2"}) {
		t.Fatalf("missing = %v", batch.MissingSessionIDs)
	}

	if _, err := client.BatchGetSessionCosts(context.Background(), []string{"session-1"}, " workspace-1 "); err != nil {
		t.Fatalf("BatchGetSessionCosts: %v", err)
	}
	if engine.batchRequest.GetWorkspaceId() != "workspace-1" {
		t.Fatalf("workspace id = %q", engine.batchRequest.GetWorkspaceId())
	}
}
