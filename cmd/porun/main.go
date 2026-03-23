package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/neatflowcv/porun"
)

type runtimeFactory func(context.Context, string) (porun.Runtime, error)

type App struct {
	stdout         io.Writer
	stderr         io.Writer
	detectHost     func() (string, error)
	newRuntime     runtimeFactory
	now            func() time.Time
	commandTimeout time.Duration
}

type CLI struct {
	Host   string    `help:"Podman service host. Defaults to CONTAINER_HOST or auto-detected socket."`
	List   listCmd   `cmd:""                                                                          help:"List containers."`
	Create createCmd `cmd:""                                                                          help:"Create a container."`
	Delete deleteCmd `cmd:""                                                                          help:"Delete a container."`
	Start  startCmd  `cmd:""                                                                          help:"Start a container."`
	Logs   logsCmd   `cmd:""                                                                          help:"Show container logs."`
}

type listCmd struct{}

type createCmd struct {
	Image   string   `help:"Container image."                               placeholder:"IMAGE"                                 required:""`
	Name    string   `help:"Container name. Defaults to porun-<timestamp>."`
	Command []string `arg:""                                                help:"Optional command. Defaults to image command." name:"command" optional:"" passthrough:""`
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

func main() {
	app := newApp(os.Stdout, os.Stderr)

	err := run(app, os.Args[1:])
	if err != nil {
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
		commandTimeout: 2 * time.Minute,
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
		return err
	}

	ctx, err := parser.Parse(args)
	if err != nil {
		return err
	}

	return ctx.Run(app)
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
		return err
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
	}
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("porun-%d", app.now().UnixNano())
	}

	containerID, err := runtime.CreateContainer(ctx, spec)
	if err != nil {
		return err
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

	if err := runtime.RemoveContainer(ctx, cmd.Container); err != nil {
		return err
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

	if err := runtime.StartContainer(ctx, cmd.Container); err != nil {
		return err
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
		return err
	}

	writeString(app.stdout, logs)

	if logs != "" && !strings.HasSuffix(logs, "\n") {
		writeString(app.stdout, "\n")
	}

	return nil
}

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

func newPodmanRuntime(ctx context.Context, host string) (porun.Runtime, error) {
	return porun.NewPodmanRuntime(ctx, host)
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
