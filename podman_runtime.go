package porun

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	"github.com/containers/podman/v5/pkg/specgen"
)

const cleanupTimeout = 15 * time.Second

var _ Runtime = (*PodmanRuntime)(nil)

type PodmanRuntime struct {
	uri string
}

func NewPodmanRuntime(ctx context.Context, uri string) (*PodmanRuntime, error) {
	_, err := bindings.NewConnection(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("connect to podman service: %w", err)
	}

	return &PodmanRuntime{uri: uri}, nil
}

func (r *PodmanRuntime) EnsureImageAvailable(ctx context.Context, image string) error {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return err
	}

	exists, err := images.Exists(connCtx, image, nil)
	if err != nil {
		return fmt.Errorf("check image %s: %w", image, err)
	}

	if exists {
		return nil
	}

	_, err = images.Pull(connCtx, image, nil)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}

	return nil
}

func (r *PodmanRuntime) ListContainers(ctx context.Context) ([]ContainerSummary, error) {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return nil, err
	}

	all := true
	options := &containers.ListOptions{
		All:       &all,
		External:  nil,
		Filters:   nil,
		Last:      nil,
		Namespace: nil,
		Size:      nil,
		Sync:      nil,
	}

	list, err := containers.List(connCtx, options)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	summaries := make([]ContainerSummary, 0, len(list))
	for _, item := range list {
		summaries = append(summaries, ContainerSummary{
			ID:     item.ID,
			Names:  item.Names,
			Image:  item.Image,
			State:  item.State,
			Status: item.Status,
		})
	}

	return summaries, nil
}

func (r *PodmanRuntime) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return "", err
	}

	containerSpec := specgen.NewSpecGenerator(spec.Image, false)
	containerSpec.Name = spec.Name
	containerSpec.Command = spec.Command
	containerSpec.Terminal = new(bool)
	containerSpec.Remove = new(bool)

	response, err := containers.CreateWithSpec(connCtx, containerSpec, nil)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", spec.Name, err)
	}

	return response.ID, nil
}

func (r *PodmanRuntime) StartContainer(ctx context.Context, containerID string) error {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return err
	}

	err = containers.Start(connCtx, containerID, nil)
	if err != nil {
		return fmt.Errorf("start container %s: %w", containerID, err)
	}

	return nil
}

func (r *PodmanRuntime) GetContainerLogs(ctx context.Context, containerID string) (string, error) {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return "", err
	}

	stdoutCh := make(chan string)
	stderrCh := make(chan string)
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go collectLogOutput(stdoutCh, stderrCh, resultCh)
	go streamContainerLogs(connCtx, containerID, stdoutCh, stderrCh, errCh)

	logs := <-resultCh

	err = <-errCh
	if err != nil {
		return "", fmt.Errorf("read logs for container %s: %w", containerID, err)
	}

	return logs, nil
}

func (r *PodmanRuntime) WaitForContainer(ctx context.Context, containerID string) (int32, error) {
	connCtx, err := r.connectionContext(ctx)
	if err != nil {
		return 0, err
	}

	exitCode, err := containers.Wait(connCtx, containerID, nil)
	if err != nil {
		return 0, fmt.Errorf("wait for container %s: %w", containerID, err)
	}

	return exitCode, nil
}

func (r *PodmanRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()

	connCtx, err := r.connectionContext(cleanupCtx)
	if err != nil {
		return err
	}

	options := &containers.RemoveOptions{
		Depend:  nil,
		Ignore:  nil,
		Force:   new(true),
		Volumes: nil,
		Timeout: nil,
	}

	_, err = containers.Remove(connCtx, containerID, options)
	if err != nil {
		return fmt.Errorf("remove container %s: %w", containerID, err)
	}

	return nil
}

func (r *PodmanRuntime) connectionContext(ctx context.Context) (context.Context, error) {
	connCtx, err := bindings.NewConnection(ctx, r.uri)
	if err != nil {
		return nil, fmt.Errorf("connect to podman service: %w", err)
	}

	return connCtx, nil
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
