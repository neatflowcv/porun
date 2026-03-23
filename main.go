package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	"github.com/containers/podman/v5/pkg/specgen"
)

const (
	imageName        = "docker.io/library/alpine:latest"
	containerLogLine = "hello from podman-go"
	runTimeout       = 2 * time.Minute
	cleanupTimeout   = 15 * time.Second
)

var (
	errContainerFailed = errors.New("container exited with non-zero code")
	errNoPodmanSocket  = errors.New("no Podman socket found")
)

func main() {
	err := run()
	if err != nil {
		writeLinef(os.Stderr, "error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	connCtx, uri, err := newRunner(ctx)
	if err != nil {
		return err
	}

	writeLinef(os.Stdout, "connecting to %s", uri)

	err = ensureImage(connCtx, imageName)
	if err != nil {
		return err
	}

	containerID, err := createAndStart(connCtx)
	if err != nil {
		return err
	}

	defer cleanupContainer(context.Background(), uri, containerID)

	logs, exitCode, err := waitForCompletion(connCtx, containerID)
	if err != nil {
		return err
	}

	return reportResult(logs, exitCode)
}

func newRunner(ctx context.Context) (context.Context, string, error) {
	uri, err := detectPodmanURI()
	if err != nil {
		return nil, "", err
	}

	connCtx, err := bindings.NewConnection(ctx, uri)
	if err != nil {
		return nil, "", fmt.Errorf("connect to podman service: %w", err)
	}

	return connCtx, uri, nil
}

func createAndStart(ctx context.Context) (string, error) {
	containerName := fmt.Sprintf("porun-%d", time.Now().UnixNano())

	containerID, err := createContainer(ctx, containerName)
	if err != nil {
		return "", err
	}

	writeLinef(os.Stdout, "created container %s (%s)", containerName, containerID)

	err = containers.Start(ctx, containerID, nil)
	if err != nil {
		return "", fmt.Errorf("start container %s: %w", containerID, err)
	}

	writeLinef(os.Stdout, "started container %s", containerID)

	return containerID, nil
}

func detectPodmanURI() (string, error) {
	if host := os.Getenv("CONTAINER_HOST"); host != "" {
		return host, nil
	}

	candidates := []string{}

	currentUser, err := user.Current()
	if err == nil {
		candidates = append(candidates, filepath.Join("/run/user", currentUser.Uid, "podman/podman.sock"))
	}

	candidates = append(candidates, "/run/podman/podman.sock")

	for _, candidate := range candidates {
		statErr := checkSocket(candidate)
		if statErr == nil {
			return "unix://" + candidate, nil
		}
	}

	return "", fmt.Errorf(
		"%w; set CONTAINER_HOST or start podman service, for example: systemctl --user start podman.socket",
		errNoPodmanSocket,
	)
}

func checkSocket(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	return nil
}

func ensureImage(ctx context.Context, image string) error {
	exists, err := images.Exists(ctx, image, nil)
	if err != nil {
		return fmt.Errorf("check image %s: %w", image, err)
	}

	if exists {
		writeLinef(os.Stdout, "image already present: %s", image)

		return nil
	}

	writeLinef(os.Stdout, "pulling image %s", image)

	_, err = images.Pull(ctx, image, nil)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}

	return nil
}

func createContainer(ctx context.Context, name string) (string, error) {
	spec := specgen.NewSpecGenerator(imageName, false)
	spec.Name = name
	spec.Command = []string{"sh", "-c", "echo " + containerLogLine}
	spec.Terminal = new(false)
	spec.Remove = new(false)

	response, err := containers.CreateWithSpec(ctx, spec, nil)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", name, err)
	}

	return response.ID, nil
}

func collectLogs(ctx context.Context, containerID string) (string, error) {
	stdoutCh := make(chan string)
	stderrCh := make(chan string)
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go collectLogOutput(stdoutCh, stderrCh, resultCh)
	go streamContainerLogs(ctx, containerID, stdoutCh, stderrCh, errCh)

	logs := <-resultCh

	err := <-errCh
	if err != nil {
		return "", err
	}

	return logs, nil
}

func collectLogOutput(stdoutCh, stderrCh <-chan string, resultCh chan<- string) {
	var builder strings.Builder

	for stdoutCh != nil || stderrCh != nil {
		select {
		case line, ok := <-stdoutCh:
			if !ok {
				stdoutCh = nil

				continue
			}

			builder.WriteString(line)
		case line, ok := <-stderrCh:
			if !ok {
				stderrCh = nil

				continue
			}

			builder.WriteString(line)
		}
	}

	resultCh <- builder.String()
}

func streamContainerLogs(
	ctx context.Context,
	containerID string,
	stdoutCh, stderrCh chan string,
	errCh chan<- error,
) {
	options := &containers.LogOptions{
		Follow:     nil,
		Since:      nil,
		Stdout:     new(true),
		Stderr:     new(true),
		Tail:       nil,
		Timestamps: nil,
		Until:      nil,
	}

	logErr := containers.Logs(ctx, containerID, options, stdoutCh, stderrCh)
	close(stdoutCh)
	close(stderrCh)

	if logErr != nil {
		errCh <- fmt.Errorf("read logs for container %s: %w", containerID, logErr)

		return
	}

	errCh <- nil
}

func waitForCompletion(ctx context.Context, containerID string) (string, int32, error) {
	logs, err := collectLogs(ctx, containerID)
	if err != nil {
		return "", 0, err
	}

	exitCode, err := containers.Wait(ctx, containerID, nil)
	if err != nil {
		return "", 0, fmt.Errorf("wait for container %s: %w", containerID, err)
	}

	return logs, exitCode, nil
}

func reportResult(logs string, exitCode int32) error {
	writeLinef(os.Stdout, "container exited with code %d", exitCode)

	if logs != "" {
		writeString(os.Stdout, "logs:\n")
		writeString(os.Stdout, logs)

		if !strings.HasSuffix(logs, "\n") {
			writeString(os.Stdout, "\n")
		}
	}

	if exitCode != 0 {
		return fmt.Errorf("%w %d", errContainerFailed, exitCode)
	}

	return nil
}

func cleanupContainer(ctx context.Context, uri, containerID string) {
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()

	connCtx, err := bindings.NewConnection(cleanupCtx, uri)
	if err != nil {
		writeLinef(os.Stderr, "cleanup: reconnect to podman: %v", err)

		return
	}

	options := &containers.RemoveOptions{
		Depend:  nil,
		Ignore:  nil,
		Force:   new(true),
		Volumes: nil,
		Timeout: nil,
	}

	_, err = containers.Remove(connCtx, containerID, options)
	if err != nil && !isMissingContainerError(err) {
		writeLinef(os.Stderr, "cleanup: remove container %s: %v", containerID, err)
	}
}

func isMissingContainerError(err error) bool {
	if err == nil {
		return false
	}

	message := err.Error()

	return errors.Is(err, os.ErrNotExist) ||
		strings.Contains(message, "no such container") ||
		strings.Contains(message, "404")
}

func writeLinef(file *os.File, format string, args ...any) {
	_, _ = file.WriteString(fmt.Sprintf(format, args...) + "\n")
}

func writeString(file *os.File, value string) {
	_, _ = file.WriteString(value)
}
