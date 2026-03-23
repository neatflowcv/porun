package porun

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

var ErrNoPodmanSocket = errors.New("no Podman socket found")

func DetectPodmanURI() (string, error) {
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
		ErrNoPodmanSocket,
	)
}

func checkSocket(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	return nil
}
