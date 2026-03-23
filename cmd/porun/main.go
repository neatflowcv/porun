package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/neatflowcv/porun"
)

const defaultCommandTimeout = 2 * time.Minute

type runtimeFactory func(context.Context, string) (porun.Runtime, error)

type App struct {
	stdout         io.Writer
	stderr         io.Writer
	detectHost     func() (string, error)
	newRuntime     runtimeFactory
	now            func() time.Time
	commandTimeout time.Duration
}

//nolint:lll // kong struct tags are long and clearer when kept on the field they describe.
type CLI struct {
	Host   string    `help:"Podman service host. Defaults to CONTAINER_HOST or auto-detected socket."`
	List   listCmd   `cmd:""                                                                          help:"List containers."`
	Create createCmd `cmd:""                                                                          help:"Create a container."`
	Delete deleteCmd `cmd:""                                                                          help:"Delete a container."`
	Start  startCmd  `cmd:""                                                                          help:"Start a container."`
	Logs   logsCmd   `cmd:""                                                                          help:"Show container logs."`
	Exec   execCmd   `cmd:""                                                                          help:"Run a shell command in a container."`
}

type listCmd struct{}

//nolint:lll // kong struct tags are long and clearer when kept on the field they describe.
type createCmd struct {
	Image   string   `help:"Container image."                                                  placeholder:"IMAGE"                                 required:""`
	Name    string   `help:"Container name. Defaults to porun-<timestamp>."`
	Volumes []string `help:"Bind mount or volume in Podman format. Repeat to add more mounts." short:"v"`
	Command []string `arg:""                                                                   help:"Optional command. Defaults to image command." name:"command" optional:"" passthrough:""`
}

type deleteCmd struct {
	Container string `arg:"" help:"Container ID or name." name:"container"`
}

type startCmd struct {
	Container string `arg:"" help:"Container ID or name." name:"container"`
}

type logsCmd struct {
	Container string `arg:"" help:"Container ID or name." name:"container"`
}

type execCmd struct {
	Container string `arg:"" help:"Container ID or name."                      name:"container"`
	Command   string `arg:"" help:"Shell command to run inside the container." name:"command"`
}

type exitStatusError struct {
	code int
}

func (e *exitStatusError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.code)
}

func main() {
	app := newApp(os.Stdout, os.Stderr)

	err := run(app, os.Args[1:])
	if err != nil {
		var exitErr *exitStatusError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}

		writeLinef(app.stderr, "error: %v", err)
		os.Exit(1)
	}
}

func newApp(stdout, stderr io.Writer) *App {
	return &App{
		stdout:         stdout,
		stderr:         stderr,
		detectHost:     porun.DetectPodmanURI,
		newRuntime:     newPodmanRuntime,
		now:            time.Now,
		commandTimeout: defaultCommandTimeout,
	}
}

func run(app *App, args []string) error {
	var cli CLI

	parser, err := kong.New(
		&cli,
		kong.Name("porun"),
		kong.Description("Podman runtime helper CLI."),
		kong.Bind(app),
	)
	if err != nil {
		return fmt.Errorf("build cli parser: %w", err)
	}

	ctx, err := parser.Parse(args)
	if err != nil {
		return fmt.Errorf("parse cli args: %w", err)
	}

	err = ctx.Run(app)
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}

	return nil
}

func (cmd *listCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	containers, err := runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	printContainerSummaries(app.stdout, containers)

	return nil
}

func (cmd *createCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	spec := porun.ContainerSpec{
		Name:    cmd.Name,
		Image:   cmd.Image,
		Command: cmd.Command,
		Volumes: cmd.Volumes,
	}
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("porun-%d", app.now().UnixNano())
	}

	containerID, err := runtime.CreateContainer(ctx, spec)
	if err != nil {
		return fmt.Errorf("create container %s: %w", spec.Name, err)
	}

	writeLinef(app.stdout, "created %s (%s)", spec.Name, containerID)

	return nil
}

func (cmd *deleteCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	err = runtime.RemoveContainer(ctx, cmd.Container)
	if err != nil {
		return fmt.Errorf("delete container %s: %w", cmd.Container, err)
	}

	writeLinef(app.stdout, "deleted %s", cmd.Container)

	return nil
}

func (cmd *startCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	err = runtime.StartContainer(ctx, cmd.Container)
	if err != nil {
		return fmt.Errorf("start container %s: %w", cmd.Container, err)
	}

	writeLinef(app.stdout, "started %s", cmd.Container)

	return nil
}

func (cmd *logsCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	logs, err := runtime.GetContainerLogs(ctx, cmd.Container)
	if err != nil {
		return fmt.Errorf("get logs for container %s: %w", cmd.Container, err)
	}

	writeString(app.stdout, logs)

	if logs != "" && !strings.HasSuffix(logs, "\n") {
		writeString(app.stdout, "\n")
	}

	return nil
}

func (cmd *execCmd) Run(app *App, cli *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.commandTimeout)
	defer cancel()

	runtime, err := app.runtime(ctx, cli.Host)
	if err != nil {
		return err
	}

	stdout, stderr, exitCode, err := runtime.ExecContainer(ctx, cmd.Container, cmd.Command)
	if err != nil {
		return fmt.Errorf("exec in container %s: %w", cmd.Container, err)
	}

	writeString(app.stdout, stdout)
	writeString(app.stderr, stderr)

	if exitCode != 0 {
		return &exitStatusError{code: exitCode}
	}

	return nil
}

//nolint:ireturn // The CLI is intentionally wired against the library runtime interface for testability.
func (app *App) runtime(ctx context.Context, host string) (porun.Runtime, error) {
	resolvedHost := host
	if resolvedHost == "" {
		detectedHost, err := app.detectHost()
		if err != nil {
			return nil, err
		}

		resolvedHost = detectedHost
	}

	return app.newRuntime(ctx, resolvedHost)
}

//nolint:ireturn // The CLI factory returns the library runtime interface used by command handlers.
func newPodmanRuntime(ctx context.Context, host string) (porun.Runtime, error) {
	runtime, err := porun.NewPodmanRuntime(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("create podman runtime: %w", err)
	}

	return runtime, nil
}

func printContainerSummaries(out io.Writer, summaries []porun.ContainerSummary) {
	if len(summaries) == 0 {
		writeString(out, "containers:\n")
		writeString(out, "  (none)\n")

		return
	}

	writeString(out, "containers:\n")

	for _, summary := range summaries {
		writeLinef(
			out,
			"  - %s | %s | %s | %s",
			shortContainerID(summary.ID),
			formatContainerNames(summary.Names),
			summary.State,
			summary.Image,
		)
	}
}

func shortContainerID(containerID string) string {
	const shortIDLength = 12
	if len(containerID) <= shortIDLength {
		return containerID
	}

	return containerID[:shortIDLength]
}

func formatContainerNames(names []string) string {
	if len(names) == 0 {
		return "(unnamed)"
	}

	return strings.Join(names, ",")
}

func writeLinef(out io.Writer, format string, args ...any) {
	_, _ = io.WriteString(out, fmt.Sprintf(format, args...)+"\n")
}

func writeString(out io.Writer, value string) {
	_, _ = io.WriteString(out, value)
}
