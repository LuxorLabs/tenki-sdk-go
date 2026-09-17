package workspace

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	rpc "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/cloud/workspace/v1beta1/workspacev1beta1connect"
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
		token = os.Getenv("TENKI_AUTH_TOKEN")
	}
	if token == "" {
		token = os.Getenv("TENKI_API_KEY")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrMissingAuthToken
	}
	if !strings.HasPrefix(token, "tk_") {
		return nil, ErrInvalidAuthToken
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("TENKI_CLOUD_API_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.tenki.cloud"
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+token)
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
