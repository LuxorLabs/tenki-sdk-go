package sandbox

import (
	"time"

	sandboxv1 "go.luxor.ai/sandbox/internal/proto/tenki/sandbox/v1"
)

// SessionCostStatus reports whether a session's cost can still change.
type SessionCostStatus string

const (
	SessionCostStatusUnspecified SessionCostStatus = "UNSPECIFIED"
	// SessionCostStatusAccruing means metering is still running; amounts can grow.
	SessionCostStatusAccruing SessionCostStatus = "ACCRUING"
	// SessionCostStatusFinal means the session has ended; amounts will not change.
	SessionCostStatusFinal SessionCostStatus = "FINAL"
	// SessionCostStatusUnavailable means the session predates per-session cost tracking.
	SessionCostStatusUnavailable SessionCostStatus = "UNAVAILABLE"
)

type SessionCostItem string

const (
	SessionCostItemUnspecified SessionCostItem = "UNSPECIFIED"
	SessionCostItemCPU         SessionCostItem = "CPU"
	SessionCostItemMemory      SessionCostItem = "MEMORY"
	// SessionCostItemDisk is disk above the included allowance.
	SessionCostItemDisk SessionCostItem = "DISK"
	// SessionCostItemPausedStorage is storage kept while the session is paused.
	SessionCostItemPausedStorage SessionCostItem = "PAUSED_STORAGE"
)

type SessionCostUnit string

const (
	SessionCostUnitUnspecified SessionCostUnit = "UNSPECIFIED"
	SessionCostUnitVCPUSecond  SessionCostUnit = "VCPU_SECOND"
	// SessionCostUnitGiBSecond uses 1 GiB = 2^30 bytes.
	SessionCostUnitGiBSecond SessionCostUnit = "GIB_SECOND"
)

type SessionCostLine struct {
	Item SessionCostItem
	// Quantity is a decimal string in Unit.
	Quantity string
	Unit     SessionCostUnit
	// AmountUSD is a decimal string in USD.
	AmountUSD string
	StartedAt time.Time
	// EndedAt is nil while the cost is accruing.
	EndedAt *time.Time
}

// SessionCost is what a session has cost so far, at list price before credits.
type SessionCost struct {
	SessionID string
	Status    SessionCostStatus
	// TotalUSD is a decimal string in USD, nil while accruing with nothing metered yet.
	TotalUSD *string
	Lines    []SessionCostLine
	// AsOf is nil until the first metering tick.
	AsOf                 *time.Time
	BilledRunningSeconds int64
	BilledPausedSeconds  int64
}

type SessionCostBatch struct {
	Costs []*SessionCost
	// MissingSessionIDs lists requested sessions that are not in the workspace or not visible to the caller.
	MissingSessionIDs []string
}

func sessionCostFromProto(cost *sandboxv1.SessionCost) *SessionCost {
	if cost == nil {
		return &SessionCost{Status: SessionCostStatusUnspecified}
	}
	lines := make([]SessionCostLine, 0, len(cost.GetLines()))
	for _, line := range cost.GetLines() {
		converted := SessionCostLine{
			Item:      sessionCostItemFromProto(line.GetItem()),
			Quantity:  line.GetQuantity(),
			Unit:      sessionCostUnitFromProto(line.GetUnit()),
			AmountUSD: line.GetAmountUsd(),
			EndedAt:   timeFromProto(line.GetEndedAt()),
		}
		if line.GetStartedAt() != nil {
			converted.StartedAt = line.GetStartedAt().AsTime()
		}
		lines = append(lines, converted)
	}
	return &SessionCost{
		SessionID:            cost.GetSessionId(),
		Status:               sessionCostStatusFromProto(cost.GetStatus()),
		TotalUSD:             cost.TotalUsd,
		Lines:                lines,
		AsOf:                 timeFromProto(cost.GetAsOf()),
		BilledRunningSeconds: cost.GetBilledRunningSeconds(),
		BilledPausedSeconds:  cost.GetBilledPausedSeconds(),
	}
}

func sessionCostStatusFromProto(status sandboxv1.SessionCostStatus) SessionCostStatus {
	switch status {
	case sandboxv1.SessionCostStatus_SESSION_COST_STATUS_ACCRUING:
		return SessionCostStatusAccruing
	case sandboxv1.SessionCostStatus_SESSION_COST_STATUS_FINAL:
		return SessionCostStatusFinal
	case sandboxv1.SessionCostStatus_SESSION_COST_STATUS_UNAVAILABLE:
		return SessionCostStatusUnavailable
	default:
		return SessionCostStatusUnspecified
	}
}

func sessionCostItemFromProto(item sandboxv1.SessionCostItem) SessionCostItem {
	switch item {
	case sandboxv1.SessionCostItem_SESSION_COST_ITEM_CPU:
		return SessionCostItemCPU
	case sandboxv1.SessionCostItem_SESSION_COST_ITEM_MEMORY:
		return SessionCostItemMemory
	case sandboxv1.SessionCostItem_SESSION_COST_ITEM_DISK:
		return SessionCostItemDisk
	case sandboxv1.SessionCostItem_SESSION_COST_ITEM_PAUSED_STORAGE:
		return SessionCostItemPausedStorage
	default:
		return SessionCostItemUnspecified
	}
}

func sessionCostUnitFromProto(unit sandboxv1.SessionCostUnit) SessionCostUnit {
	switch unit {
	case sandboxv1.SessionCostUnit_SESSION_COST_UNIT_VCPU_SECOND:
		return SessionCostUnitVCPUSecond
	case sandboxv1.SessionCostUnit_SESSION_COST_UNIT_GIB_SECOND:
		return SessionCostUnitGiBSecond
	default:
		return SessionCostUnitUnspecified
	}
}
