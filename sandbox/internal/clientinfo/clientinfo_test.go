package clientinfo

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	t.Parallel()

	dep := func(version string) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
			Deps: []*debug.Module{{Path: "example.com/other", Version: "v9.9.9"}, {Path: modulePath, Version: version}},
		}
	}
	replaced := dep("v0.0.0")
	replaced.Deps[1].Replace = &debug.Module{Path: "./_sdk"}
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"tagged dependency", dep("v1.4.1"), true, "1.4.1"},
		{"prerelease dependency", dep("v1.5.0-rc.1"), true, "1.5.0-rc.1"},
		{"replaced dependency", replaced, true, "unknown"},
		{"main module", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.4.1"}}, true, "1.4.1"},
		{"main module in development", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, true, "unknown"},
		{"module absent", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}}, true, "unknown"},
		{"no build info", nil, false, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := moduleVersion(tt.info, tt.ok); got != tt.want {
				t.Fatalf("moduleVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGoRuntime(t *testing.T) {
	t.Parallel()

	for version, want := range map[string]string{
		"go1.27.1":                           "go/1.27.1",
		"go1.27rc1":                          "go/1.27rc1",
		"go1.26.3-X:jsonv2":                  "go/1.26.3",
		"go1.27.1-X:nogreenteagc":            "go/1.27.1",
		"devel go1.27-abcdef Tue Oct 1 2026": "go/devel",
	} {
		if got := goRuntime(version); got != want {
			t.Errorf("goRuntime(%q) = %q, want %q", version, got, want)
		}
	}
}
