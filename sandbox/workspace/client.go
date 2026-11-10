package workspace

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/clientinfo"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
	"github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/sdkmeta"
)

type WorkspaceOptions struct {
	WorkspaceID, AuthToken, BaseURL string
	HTTPClient                      *http.Client
}

// WorkspaceClient owns workspace scope, authentication, and connection lifetime.
type WorkspaceClient struct {
	Secrets        *Secrets
	httpClient     *http.Client
	ownsHTTPClient bool
}

func NewWorkspaceClient(options WorkspaceOptions) (*WorkspaceClient, error) {
	token := options.AuthToken
	if token == "" {
		token = sdkmeta.Getenv(sdkmeta.AuthTokenEnvs)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrMissingAuthToken
	}
	if !sdkmeta.HasAPIKeyPrefix(token) {
		return nil, ErrInvalidAuthToken
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = sdkmeta.Getenv(sdkmeta.CloudAPIURLEnvs)
	}
	if baseURL == "" {
		baseURL = sdkmeta.DefaultBaseURL
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+token)
			sdkmeta.SetRequestIdentity(request.Header())
			clientinfo.SetHeaders(request.Header())
			return next(ctx, request)
		}
	})
	return &WorkspaceClient{
		httpClient:     client,
		ownsHTTPClient: options.HTTPClient == nil,
		Secrets: &Secrets{
			workspaceID: options.WorkspaceID,
			rpc:         rpc.NewWorkspaceSecretsServiceClient(client, baseURL, connect.WithInterceptors(auth)),
		},
	}, nil
}

// Close releases this client's idle HTTP connections; supplied clients remain caller-owned.
func (c *WorkspaceClient) Close() error {
	if c.ownsHTTPClient {
		c.httpClient.CloseIdleConnections()
	}
	return nil
}
