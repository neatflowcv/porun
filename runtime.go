package porun

import "context"

type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
}

type ContainerSummary struct {
	ID     string
	Names  []string
	Image  string
	State  string
	Status string
}

type Runtime interface {
	EnsureImageAvailable(ctx context.Context, image string) error
	ListContainers(ctx context.Context) ([]ContainerSummary, error)
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	ExecContainer(ctx context.Context, containerID, command string) (stdout string, stderr string, exitCode int, err error)
	GetContainerLogs(ctx context.Context, containerID string) (string, error)
	WaitForContainer(ctx context.Context, containerID string) (int32, error)
	RemoveContainer(ctx context.Context, containerID string) error
}
