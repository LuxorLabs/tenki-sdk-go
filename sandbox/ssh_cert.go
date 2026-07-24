package sandbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// SSHCert is the result of IssueSandboxSSHCert. SSHCert is the OpenSSH cert
// in authorized_keys format (begins with "ssh-ed25519-cert-v01@openssh.com").
// CAPub is the user-CA public key for known_hosts pinning. ExpiresAt is when
// the cert stops being valid (gateway rejects after this).
type SSHCert struct {
	SSHCert    string
	CAPub      string
	ExpiresAt  time.Time
	CertSerial string
}

// IssueSandboxSSHCert asks the engine to sign an SSH user cert for the
// given session. publicKey is OpenSSH-format (e.g. "ssh-ed25519 AAAA...").
// ttl caps the cert validity (server clamps to policy max; pass 0 for the
// server default).
//
// Use the returned SSHCert.SSHCert as the `CertificateFile` for OpenSSH, or
// write it to disk alongside the private key as `<key>-cert.pub` so ssh
// auto-loads it.
func (c *Client) IssueSandboxSSHCert(ctx context.Context, sessionID, publicKey string, ttl time.Duration) (*SSHCert, error) {
	if c == nil || c.sshGateway == nil {
		return nil, errors.New("sandbox: client not initialized")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("sandbox: session_id required")
	}
	if strings.TrimSpace(publicKey) == "" {
		return nil, errors.New("sandbox: public_key required")
	}

	req := connect.NewRequest(&sandboxv1.IssueSandboxSSHCertRequest{
		SessionId: sessionID,
		PublicKey: strings.TrimSpace(publicKey),
	})
	if ttl > 0 {
		req.Msg.RequestedTtl = durationpb.New(ttl)
	}

	resp, err := c.sshGateway.IssueSandboxSSHCert(ctx, req)
	if err != nil {
		return nil, err
	}

	out := &SSHCert{
		SSHCert:    resp.Msg.GetSshCert(),
		CAPub:      resp.Msg.GetCaPub(),
		CertSerial: resp.Msg.GetCertSerial(),
	}
	if t := resp.Msg.GetExpiresAt(); t != nil {
		out.ExpiresAt = t.AsTime()
	}
	return out, nil
}
