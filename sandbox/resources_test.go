package sandbox

import (
	"context"
	"errors"
	"testing"
)

func TestCreateRejectsInvalidResources(t *testing.T) {
	t.Parallel()

	client, err := New(WithAuthToken("tk_test"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	for _, tc := range []struct {
		name string
		opt  CreateOption
		want string
	}{
		{name: "cpu", opt: WithCPUCores(129), want: "cpu_cores must be between 1 and 128"},
		{name: "memory", opt: WithMemoryMB(8089), want: "memory_mb must be aligned to 2 MiB"},
		{name: "disk", opt: WithDiskSizeGB(3), want: "disk_size_gb must be between 5 and 100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := client.Create(context.Background(), tc.opt)
			if !errors.Is(err, ErrInvalidResourceConfig) {
				t.Fatalf("Create error = %v, want ErrInvalidResourceConfig", err)
			}
			if got := err.Error(); got != "sandbox: invalid resource configuration: "+tc.want {
				t.Fatalf("Create error = %q, want %q", got, "sandbox: invalid resource configuration: "+tc.want)
			}
		})
	}
}

func TestTemplateRejectsInvalidResources(t *testing.T) {
	t.Parallel()

	client, err := New(WithAuthToken("tk_test"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	for _, tc := range []struct {
		name string
		opts []TemplateOption
	}{
		{
			name: "memory",
			opts: []TemplateOption{
				WithTemplateResources(4, 8089),
			},
		},
		{
			name: "memory below provision floor",
			opts: []TemplateOption{
				WithTemplateResources(4, 128),
			},
		},
		{
			name: "disk",
			opts: []TemplateOption{
				WithTemplateResources(4, 4096),
				WithTemplateDiskSizeGB(4),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]TemplateOption{
				WithWorkspaceID("019f191e-020e-7f73-a647-498d954be5e6"),
				WithTemplateName("invalid"),
			}, tc.opts...)

			_, err := client.CreateTemplate(context.Background(), opts...)
			if !errors.Is(err, ErrInvalidResourceConfig) {
				t.Fatalf("CreateTemplate error = %v, want ErrInvalidResourceConfig", err)
			}
		})
	}
}
