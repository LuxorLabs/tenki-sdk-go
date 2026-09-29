package sandbox

import (
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/proto"
)

type SecretRequestBinding = sandboxv1.SecretRequestBinding

func cloneSecretRequests(rules []*SecretRequestBinding) []*SecretRequestBinding {
	out := make([]*SecretRequestBinding, len(rules))
	for i, r := range rules {
		if r != nil {
			out[i] = proto.CloneOf(r)
		}
	}
	return out
}

// WithSecretRequests declares HTTPS request injection for a direct session launch.
func WithSecretRequests(rules ...*SecretRequestBinding) CreateOption {
	snapshot := cloneSecretRequests(rules)
	return createOptionFunc(func(cfg *createConfig) { cfg.secretRequests = cloneSecretRequests(snapshot) })
}
