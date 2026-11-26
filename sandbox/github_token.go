package sandbox

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const githubAPICredential = "SDK_GITHUB_API"
const githubGitCredential = "SDK_GITHUB_GIT"

func githubInjectionCredentials(token string) ([]InjectionCredentialInput, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("sandbox: GitHub token injection requires a nonempty github token")
	}
	return []InjectionCredentialInput{
		{Name: githubAPICredential, Value: []byte(token), Rules: []InjectionRule{{Origin: "https://api.github.com", Methods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, PathPrefix: "/", Header: "Authorization"}}},
		{Name: githubGitCredential, Value: []byte(base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))), Rules: []InjectionRule{{Origin: "https://github.com", Methods: []string{"GET", "POST"}, PathPrefix: "/", Header: "Authorization"}}},
	}, nil
}

func githubInjectionEnv(env map[string]string) error {
	if host := env["GH_HOST"]; host != "" && host != "github.com" {
		return errors.New("sandbox: GitHub token injection supports github.com only")
	}
	for key := range env {
		if strings.HasPrefix(key, "GIT_CONFIG") {
			return errors.New("sandbox: GitHub token injection reserves GIT_CONFIG environment variables")
		}
	}
	env["GH_TOKEN"] = "secrets://" + githubAPICredential
	env["GIT_TOKEN"] = env["GH_TOKEN"]
	settings := [][2]string{
		{"http.https://github.com/.extraHeader", "Authorization: Basic secrets://" + githubGitCredential},
		{"http.https://github.com/.version", "HTTP/1.1"},
		{"credential.https://github.com.helper", ""},
		{"http.https://github.com/.followRedirects", "false"},
	}
	env["GIT_CONFIG_COUNT"] = strconv.Itoa(len(settings))
	for i, setting := range settings {
		env["GIT_CONFIG_KEY_"+strconv.Itoa(i)] = setting[0]
		env["GIT_CONFIG_VALUE_"+strconv.Itoa(i)] = setting[1]
	}
	return nil
}
