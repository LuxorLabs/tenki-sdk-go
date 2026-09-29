package sandbox

// WithSecretPolicies selects saved workspace policies for request injection.
func WithSecretPolicies(names ...string) CreateOption {
	snapshot := append([]string(nil), names...)
	return createOptionFunc(func(cfg *createConfig) { cfg.secretPolicies = append([]string(nil), snapshot...) })
}
