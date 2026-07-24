package sandbox

import (
	"context"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

// Identity represents the authenticated caller's identity.
type Identity struct {
	OwnerType  string
	OwnerID    string
	Workspaces []IdentityWorkspace
}

// IdentityWorkspace is a workspace returned by WhoAmI.
type IdentityWorkspace struct {
	ID   string
	Name string
}

// WhoAmI returns the authenticated caller's identity.
func (c *Client) WhoAmI(ctx context.Context) (*Identity, error) {
	resp, err := c.sandbox.WhoAmI(ctx, connect.NewRequest(&sandboxv1.WhoAmIRequest{}))
	if err != nil {
		return nil, mapError(err)
	}
	id := &Identity{
		OwnerType: resp.Msg.OwnerType,
		OwnerID:   resp.Msg.OwnerId,
	}
	for _, ws := range resp.Msg.Workspaces {
		iws := IdentityWorkspace{
			ID:   ws.WorkspaceId,
			Name: ws.Name,
		}
		id.Workspaces = append(id.Workspaces, iws)
	}
	return id, nil
}
