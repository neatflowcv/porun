package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/neatflowcv/porun"
)

const testContainerID = "abc123"

var errBoom = errors.New("boom")

func TestRunListUsesDetectedHost(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	runtime.containers = []porun.ContainerSummary{
		newContainerSummary("1234567890abcdef", []string{"demo"}, "running", "alpine:latest"),
	}

	app, stdout, _ := newTestApp(runtime)
	app.detectHost = func() (string, error) {
		return "unix:///detected.sock", nil
	}

	err := run(app, []string{"list"})
	if err != nil {
		t.Fatalf("run list: %v", err)
	}

	if runtime.host != "unix:///detected.sock" {
		t.Fatalf("host = %q, want detected host", runtime.host)
	}

	output := stdout.String()
	if !strings.Contains(output, "1234567890ab | demo | running | alpine:latest") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCreateUsesExplicitHostAndGeneratedName(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	runtime.createID = "container-123"
	app, stdout, _ := newTestApp(runtime)
	app.detectHost = func() (string, error) {
		t.Fatal("detectHost should not be called when --host is provided")

		return "", nil
	}
	app.now = func() time.Time {
		return time.Unix(0, 42)
	}

	args := []string{
		"--host=unix:///explicit.sock",
		"create",
		"--image", "alpine:latest",
		"echo",
		"hello",
	}

	err := run(app, args)
	if err != nil {
		t.Fatalf("run create: %v", err)
	}

	if runtime.host != "unix:///explicit.sock" {
		t.Fatalf("host = %q, want explicit host", runtime.host)
	}

	if runtime.createdSpec.Name != "porun-42" {
		t.Fatalf("created name = %q, want generated name", runtime.createdSpec.Name)
	}

	if runtime.createdSpec.Image != "alpine:latest" {
		t.Fatalf("created image = %q", runtime.createdSpec.Image)
	}

	if got := strings.Join(runtime.createdSpec.Command, " "); got != "echo hello" {
		t.Fatalf("created command = %q", got)
	}

	if !strings.Contains(stdout.String(), "created porun-42 (container-123)") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestRunCreateAllowsImageDefaultCommand(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	app, _, _ := newTestApp(runtime)

	err := run(app, []string{"create", "--image", "alpine:latest"})
	if err != nil {
		t.Fatalf("run create: %v", err)
	}

	if len(runtime.createdSpec.Command) != 0 {
		t.Fatalf("created command = %#v, want empty to use image default", runtime.createdSpec.Command)
	}
}

func TestRunDeleteStartAndLogs(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	runtime.logs = "line one"
	app, stdout, _ := newTestApp(runtime)

	for _, args := range [][]string{
		{"delete", testContainerID},
		{"start", testContainerID},
		{"logs", testContainerID},
	} {
		stdout.Reset()

		err := run(app, args)
		if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
	}

	if runtime.deleted != testContainerID {
		t.Fatalf("deleted = %q", runtime.deleted)
	}

	if runtime.started != testContainerID {
		t.Fatalf("started = %q", runtime.started)
	}

	if runtime.logged != testContainerID {
		t.Fatalf("logged = %q", runtime.logged)
	}

	if got := stdout.String(); got != "line one\n" {
		t.Fatalf("logs output = %q", got)
	}
}

func TestRunExecWritesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	runtime.execStdout = "hello\n"
	runtime.execStderr = "warn\n"
	app, stdout, stderr := newTestApp(runtime)

	err := run(app, []string{"exec", testContainerID, `echo "hello"`})
	if err != nil {
		t.Fatalf("run exec: %v", err)
	}

	if runtime.execContainer != testContainerID {
		t.Fatalf("exec container = %q", runtime.execContainer)
	}

	if runtime.execCommand != `echo "hello"` {
		t.Fatalf("exec command = %q", runtime.execCommand)
	}

	if got := stdout.String(); got != "hello\n" {
		t.Fatalf("stdout = %q", got)
	}

	if got := stderr.String(); got != "warn\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunExecReturnsExitStatusError(t *testing.T) {
	t.Parallel()

	runtime := newFakeRuntime()
	runtime.execExitCode = 17
	app, stdout, stderr := newTestApp(runtime)

	err := run(app, []string{"exec", testContainerID, "false"})
	if err == nil {
		t.Fatal("expected error")
	}

	var exitErr *exitStatusError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitStatusError, got %v", err)
	}

	if exitErr.code != 17 {
		t.Fatalf("exit code = %d", exitErr.code)
	}

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunPropagatesRuntimeFactoryError(t *testing.T) {
	t.Parallel()

	app, _, _ := newTestApp(newFakeRuntime())
	app.newRuntime = func(context.Context, string) (porun.Runtime, error) {
		return nil, errBoom
	}

	err := run(app, []string{"--host=unix:///explicit.sock", "list"})
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeRuntime struct {
	host          string
	containers    []porun.ContainerSummary
	createID      string
	createdSpec   porun.ContainerSpec
	deleted       string
	started       string
	logged        string
	logs          string
	execContainer string
	execCommand   string
	execStdout    string
	execStderr    string
	execExitCode  int
	execErr       error
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		host:          "",
		containers:    nil,
		createID:      "",
		createdSpec:   porun.ContainerSpec{Name: "", Image: "", Command: nil},
		deleted:       "",
		started:       "",
		logged:        "",
		logs:          "",
		execContainer: "",
		execCommand:   "",
		execStdout:    "",
		execStderr:    "",
		execExitCode:  0,
		execErr:       nil,
	}
}

func newContainerSummary(id string, names []string, state, image string) porun.ContainerSummary {
	return porun.ContainerSummary{
		ID:     id,
		Names:  names,
		Image:  image,
		State:  state,
		Status: "",
	}
}

func (f *fakeRuntime) EnsureImageAvailable(context.Context, string) error {
	return nil
}

func (f *fakeRuntime) ListContainers(context.Context) ([]porun.ContainerSummary, error) {
	return f.containers, nil
}

func (f *fakeRuntime) CreateContainer(_ context.Context, spec porun.ContainerSpec) (string, error) {
	f.createdSpec = spec
	if f.createID == "" {
		return "created-id", nil
	}

	return f.createID, nil
}

func (f *fakeRuntime) StartContainer(_ context.Context, containerID string) error {
	f.started = containerID

	return nil
}

func (f *fakeRuntime) GetContainerLogs(_ context.Context, containerID string) (string, error) {
	f.logged = containerID

	return f.logs, nil
}

func (f *fakeRuntime) ExecContainer(_ context.Context, containerID, command string) (string, string, int, error) {
	f.execContainer = containerID
	f.execCommand = command

	return f.execStdout, f.execStderr, f.execExitCode, f.execErr
}

func (f *fakeRuntime) WaitForContainer(context.Context, string) (int32, error) {
	return 0, nil
}

func (f *fakeRuntime) RemoveContainer(_ context.Context, containerID string) error {
	f.deleted = containerID

	return nil
}

func newTestApp(runtime *fakeRuntime) (*App, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	app := newApp(stdout, stderr)
	app.newRuntime = func(_ context.Context, host string) (porun.Runtime, error) {
		runtime.host = host

		return runtime, nil
	}
	app.detectHost = func() (string, error) {
		return "unix:///default.sock", nil
	}
	app.commandTimeout = time.Second

	return app, stdout, stderr
}
