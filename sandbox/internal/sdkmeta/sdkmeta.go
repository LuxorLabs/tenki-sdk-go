// Package sdkmeta holds defaults, environment names and request identity shared by the sandbox and workspace clients.
package sdkmeta

import (
	"net/http"
	"os"
	"runtime/debug"
	"strings"
)

const (
	DefaultBaseURL = "https://api.tenki.cloud"

	EnvLuxorAIAuthToken   = "LUXORAI_AUTH_TOKEN"
	EnvLuxorAIAPIKey      = "LUXORAI_API_KEY"
	EnvLuxorAIAPIEndpoint = "LUXORAI_API_ENDPOINT"
	EnvLuxorAIAPIURL      = "LUXORAI_API_URL"
	EnvLuxorAIGatewayURL  = "LUXORAI_SANDBOX_GATEWAY_URL"
	EnvLuxorAICloudAPIURL = "LUXORAI_CLOUD_API_URL"

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
var AuthTokenEnvs = []string{EnvLuxorAIAuthToken, EnvLuxorAIAPIKey, EnvAuthToken, EnvAPIKey}

// BaseURLEnvs lists sandbox API base URL variables in precedence order.
var BaseURLEnvs = []string{EnvLuxorAIAPIEndpoint, EnvLuxorAIAPIURL, EnvAPIEndpoint, EnvAPIURL}

// GatewayURLEnvs lists sandbox gateway URL variables in precedence order.
var GatewayURLEnvs = []string{EnvLuxorAIGatewayURL, EnvGatewayURL}

// CloudAPIURLEnvs lists workspace API base URL variables in precedence order.
var CloudAPIURLEnvs = []string{EnvLuxorAICloudAPIURL, EnvCloudAPIURL}

// Getenv returns the first non-blank value among names, trimmed.
func Getenv(names []string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
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
