package sandbox

import (
	"context"
	"strings"
	"testing"
)

// Go cannot reproduce CLO-4671: Session.Command takes a []string, so there is no
// variadic form for a list to collapse into, and a non-string argv part does not
// compile. What is left to pin is the runtime half of the same contract, which
// the Python and TypeScript SDKs now match.

func TestCommandCarriesArgvVerbatim(t *testing.T) {
	session := &Session{client: &Client{}, ID: "session-1"}
	// A part containing spaces stays one part: argv is never shell-split.
	argv := []string{"sh", "-c", "echo hello world"}
	cmd := session.Command(argv)

	if got := strings.Join(cmd.argv, "\x00"); got != strings.Join(argv, "\x00") {
		t.Fatalf("Command argv = %q, want %q", cmd.argv, argv)
	}
}

func TestCommandCopiesArgvDefensively(t *testing.T) {
	session := &Session{client: &Client{}, ID: "session-1"}
	argv := []string{"sh", "-c", "echo hi"}
	cmd := session.Command(argv)

	argv[0] = "rm"
	if cmd.argv[0] != "sh" {
		t.Fatalf("caller mutation leaked into the command: argv[0] = %q, want %q", cmd.argv[0], "sh")
	}
}

func TestStreamRejectsUnrunnableArgv(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"empty_argv0", []string{"", "-c", "echo hi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := &Session{client: &Client{}, ID: "session-1"}
			// Rejected before any dial, so the nil transport is never reached.
			_, err := session.Command(tc.argv).Stream(context.Background())
			if err == nil {
				t.Fatalf("Stream(%q) succeeded, want an error", tc.argv)
			}
			if !strings.Contains(err.Error(), "empty command") {
				t.Fatalf("Stream(%q) error = %v, want it to mention %q", tc.argv, err, "empty command")
			}
		})
	}
}
