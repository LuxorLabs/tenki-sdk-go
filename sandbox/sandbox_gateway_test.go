package sandbox

import "testing"

func TestDeriveGatewayAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "api host maps to sandbox gateway",
			baseURL: "https://api.tenki.cloud",
			want:    "https://sandbox-gateway.tenki.cloud",
		},
		{
			name:    "luxor api host maps to sandbox gateway",
			baseURL: "https://api.luxor.ai",
			want:    "https://sandbox-gateway.luxor.ai",
		},
		{
			name:    "app host maps to sandbox gateway",
			baseURL: "https://app.tenki.cloud",
			want:    "https://sandbox-gateway.tenki.cloud",
		},
		{
			name:    "multi-level host keeps suffix",
			baseURL: "https://api.staging.example.com",
			want:    "https://sandbox-gateway.staging.example.com",
		},
		{
			name:    "host keeps scheme and port",
			baseURL: "https://api.example.com:4443",
			want:    "https://sandbox-gateway.example.com:4443",
		},
		{
			name:    "non api host still prefixes sandbox gateway",
			baseURL: "https://tenki.example",
			want:    "https://sandbox-gateway.tenki.example",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := deriveGatewayAddress(tt.baseURL); got != tt.want {
				t.Fatalf("deriveGatewayAddress(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}
