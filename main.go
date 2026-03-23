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
)

const (
	imageName        = "docker.io/library/alpine:latest"
	containerLogLine = "hello from podman-go"
	runTimeout       = 2 * time.Minute
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

	uri, err := detectPodmanURI()
	if err != nil {
		return err
	}

	runtime, err := NewPodmanRuntime(ctx, uri)
	if err != nil {
		return err
	}

	writeLinef(os.Stdout, "connecting to %s", uri)

	summaries, err := runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	printContainerSummaries(summaries)

	err = runtime.EnsureImageAvailable(ctx, imageName)
	if err != nil {
		return err
	}

	containerID, err := createAndStart(ctx, runtime)
	if err != nil {
		return err
	}

	defer cleanupContainer(context.Background(), runtime, containerID)

	logs, exitCode, err := waitForCompletion(ctx, runtime, containerID)
	if err != nil {
		return err
	}

	return reportResult(logs, exitCode)
}

func createAndStart(ctx context.Context, runtime Runtime) (string, error) {
	containerName := fmt.Sprintf("porun-%d", time.Now().UnixNano())
	spec := ContainerSpec{
		Name:    containerName,
		Image:   imageName,
		Command: []string{"sh", "-c", "echo " + containerLogLine},
	}

	containerID, err := runtime.CreateContainer(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", containerName, err)
	}

	writeLinef(os.Stdout, "created container %s (%s)", containerName, containerID)

	err = runtime.StartContainer(ctx, containerID)
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

func printContainerSummaries(summaries []ContainerSummary) {
	if len(summaries) == 0 {
		writeString(os.Stdout, "containers:\n")
		writeString(os.Stdout, "  (none)\n")

		return
	}

	writeString(os.Stdout, "containers:\n")

	for _, summary := range summaries {
		writeLinef(
			os.Stdout,
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

func waitForCompletion(ctx context.Context, runtime Runtime, containerID string) (string, int32, error) {
	logs, err := runtime.GetContainerLogs(ctx, containerID)
	if err != nil {
		return "", 0, fmt.Errorf("collect logs for container %s: %w", containerID, err)
	}

	exitCode, err := runtime.WaitForContainer(ctx, containerID)
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

func cleanupContainer(ctx context.Context, runtime Runtime, containerID string) {
	err := runtime.RemoveContainer(ctx, containerID)
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
