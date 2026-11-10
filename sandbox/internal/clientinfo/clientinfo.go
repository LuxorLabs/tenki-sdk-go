// Package clientinfo identifies this SDK to Tenki on every request.
package clientinfo

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	modulePath = "github.com/LuxorLabs/tenki-sdk-go/sandbox"
	generation = "1"
)

var headers = [...][2]string{
	{"x-luxor-client-family", "go"},
	{"x-luxor-client-version", moduleVersion(debug.ReadBuildInfo())},
	{"x-luxor-client-generation", generation},
	{"x-luxor-client-runtime", goRuntime(runtime.Version())},
}

// SetHeaders sets the client metadata headers on an outgoing request.
func SetHeaders(h http.Header) {
	for _, header := range headers {
		h.Set(header[0], header[1])
	}
}

func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "unknown"
	}
	version := ""
	if info.Main.Path == modulePath {
		version = info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			// A replaced module's required version, such as an in-tree v0.0.0, is not the code that runs.
			if dep.Replace != nil {
				return "unknown"
			}
			version = dep.Version
		}
	}
	if version == "" || version == "(devel)" {
		return "unknown"
	}
	return strings.TrimPrefix(version, "v")
}

// goRuntime drops GOEXPERIMENT suffixes ("go1.26.3-X:jsonv2") and devel build details, which Tenki rejects.
func goRuntime(version string) string {
	version, _, _ = strings.Cut(strings.TrimPrefix(version, "go"), " ")
	version, _, _ = strings.Cut(version, "-X:")
	return "go/" + version
}
