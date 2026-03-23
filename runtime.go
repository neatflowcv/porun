package main

import "context"

type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
}

type Runtime interface {
	EnsureImageAvailable(ctx context.Context, image string) error
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	GetContainerLogs(ctx context.Context, containerID string) (string, error)
	WaitForContainer(ctx context.Context, containerID string) (int32, error)
	RemoveContainer(ctx context.Context, containerID string) error
}
