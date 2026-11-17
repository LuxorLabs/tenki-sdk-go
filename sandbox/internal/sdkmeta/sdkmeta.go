// Package sdkmeta holds defaults, environment names and request identity shared by the sandbox and workspace clients.
package sdkmeta

import (
	"net/http"
	"os"
	"runtime/debug"
	"strings"
)

const (
	DefaultBaseURL = "https://api.luxor.ai"

	EnvLuxorAuthToken   = "LUXOR_AUTH_TOKEN"
	EnvLuxorAPIKey      = "LUXOR_API_KEY"
	EnvLuxorAPIEndpoint = "LUXOR_API_ENDPOINT"
	EnvLuxorAPIURL      = "LUXOR_API_URL"
	EnvLuxorGatewayURL  = "LUXOR_SANDBOX_GATEWAY_URL"
	EnvLuxorCloudAPIURL = "LUXOR_CLOUD_API_URL"

	EnvAuthToken   = "TENKI_AUTH_TOKEN"
	EnvAPIKey      = "TENKI_API_KEY"
	EnvAPIEndpoint = "TENKI_API_ENDPOINT"
	EnvAPIURL      = "TENKI_API_URL"
	EnvGatewayURL  = "TENKI_SANDBOX_GATEWAY_URL"
	EnvCloudAPIURL = "TENKI_CLOUD_API_URL"

	modulePath = "go.luxor.ai/sandbox"
)

var (
	Version   = moduleVersion()
	UserAgent = "luxor-sandbox-go/" + Version
)

// AuthTokenEnvs lists auth token variables in precedence order.
var AuthTokenEnvs = []string{EnvLuxorAuthToken, EnvLuxorAPIKey, EnvAuthToken, EnvAPIKey}

// BaseURLEnvs lists sandbox API base URL variables in precedence order.
var BaseURLEnvs = []string{EnvLuxorAPIEndpoint, EnvLuxorAPIURL, EnvAPIEndpoint, EnvAPIURL}

// GatewayURLEnvs lists sandbox gateway URL variables in precedence order.
var GatewayURLEnvs = []string{EnvLuxorGatewayURL, EnvGatewayURL}

// CloudAPIURLEnvs lists workspace API base URL variables in precedence order.
var CloudAPIURLEnvs = []string{EnvLuxorCloudAPIURL, EnvCloudAPIURL}

// Getenv returns the first usable value among names, trimmed.
func Getenv(names []string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); usableEnvValue(name, value) {
			return value
		}
	}
	return ""
}

// usableEnvValue skips blanks and a LUXOR_API_KEY without an lx_ or tk_ prefix, which may be a Luxor Mining Pool key.
func usableEnvValue(name, value string) bool {
	if name == EnvLuxorAPIKey {
		return HasAPIKeyPrefix(value)
	}
	return value != ""
}

// HasAPIKeyPrefix reports whether token carries an accepted API key or service token prefix.
func HasAPIKeyPrefix(token string) bool {
	return len(token) >= 3 && (token[:3] == "lx_" || token[:3] == "tk_")
}

// SetRequestIdentity leads the User-Agent with the SDK product, keeping any existing value.
func SetRequestIdentity(header http.Header) {
	switch existing := header.Get("User-Agent"); {
	case existing == "":
		header.Set("User-Agent", UserAgent)
	case !strings.HasPrefix(existing, UserAgent):
		header.Set("User-Agent", UserAgent+" "+existing)
	}
}

func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Path == modulePath && info.Main.Version != "" {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			if dep.Replace != nil && dep.Replace.Version != "" {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return "unknown"
}
