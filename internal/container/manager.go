package container

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

const (
	NetworkName = "rally-net"
	ImageName   = "rally-ae-base:latest"
	LabelPrefix = "rally."
)

// ContainerInfo holds status information about an AE container.
type ContainerInfo struct {
	ContainerID string
	Name        string
	State       string // running, exited, etc.
	EmployeeID  string
	CompanyID   string
	Role        string
}

// Manager manages Docker containers for AE agents.
type Manager struct {
	docker       client.APIClient
	workspaceRoot string
	rallyAPIURL  string
}

// NewManager creates a new container Manager.
// workspaceRoot is the host path for workspace bind mounts (default: /var/rally/workspaces).
// rallyAPIURL is the URL AE containers use to reach Rally (e.g., http://host.docker.internal:8432).
func NewManager(workspaceRoot, rallyAPIURL string) (*Manager, error) {
	if workspaceRoot == "" {
		workspaceRoot = "/var/rally/workspaces"
	}
	if rallyAPIURL == "" {
		rallyAPIURL = "http://host.docker.internal:8432"
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	return &Manager{
		docker:       cli,
		workspaceRoot: workspaceRoot,
		rallyAPIURL:  rallyAPIURL,
	}, nil
}

// EnsureNetwork creates the rally-net bridge network if it doesn't exist.
func (m *Manager) EnsureNetwork(ctx context.Context) error {
	networks, err := m.docker.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("name", NetworkName)),
	})
	if err != nil {
		return fmt.Errorf("list networks: %w", err)
	}
	if len(networks) > 0 {
		return nil
	}

	_, err = m.docker.NetworkCreate(ctx, NetworkName, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return fmt.Errorf("create network %s: %w", NetworkName, err)
	}
	slog.Info("created docker network", "name", NetworkName)
	return nil
}

// EnsureWorkspaceDirs creates the shared and per-AE workspace directories on the host.
func (m *Manager) EnsureWorkspaceDirs(companyID, employeeID string) (sharedDir, scratchDir string, err error) {
	sharedDir = filepath.Join(m.workspaceRoot, companyID, "shared")
	scratchDir = filepath.Join(m.workspaceRoot, companyID, ".ae", employeeID)

	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir shared: %w", err)
	}
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir scratch: %w", err)
	}
	return sharedDir, scratchDir, nil
}

// CreateAndStartOpts holds the parameters for creating an AE container.
type CreateAndStartOpts struct {
	ContainerName string
	EmployeeID    string
	CompanyID     string
	CompanyName   string
	Role          string
	AEName        string
	APIToken      string
	SoulMD        string
	ConfigJSON    string // JSON-encoded EmployeeConfig
}

// CreateAndStart creates and starts a Docker container for an AE.
func (m *Manager) CreateAndStart(ctx context.Context, opts CreateAndStartOpts) (containerID string, err error) {
	sharedDir, scratchDir, err := m.EnsureWorkspaceDirs(opts.CompanyID, opts.EmployeeID)
	if err != nil {
		return "", err
	}

	if err := m.EnsureNetwork(ctx); err != nil {
		return "", err
	}

	env := []string{
		"RALLY_API_URL=" + m.rallyAPIURL,
		"RALLY_API_TOKEN=" + opts.APIToken,
		"EMPLOYEE_ID=" + opts.EmployeeID,
		"COMPANY_ID=" + opts.CompanyID,
		"AE_NAME=" + opts.AEName,
		"AE_ROLE=" + opts.Role,
		"SOUL_MD=" + opts.SoulMD,
		"AE_CONFIG=" + opts.ConfigJSON,
	}

	containerCfg := &dockercontainer.Config{
		Image: ImageName,
		Env:   env,
		Labels: map[string]string{
			LabelPrefix + "employee_id": opts.EmployeeID,
			LabelPrefix + "company_id":  opts.CompanyID,
			LabelPrefix + "role":        opts.Role,
			LabelPrefix + "name":        opts.AEName,
		},
	}

	hostCfg := &dockercontainer.HostConfig{
		Binds: []string{
			sharedDir + ":/workspace",
			scratchDir + ":/home/ae/scratch",
		},
		RestartPolicy: dockercontainer.RestartPolicy{
			Name: dockercontainer.RestartPolicyUnlessStopped,
		},
		// Allow containers to reach the host on macOS/Windows
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}

	networkCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			NetworkName: {},
		},
	}

	resp, err := m.docker.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, opts.ContainerName)
	if err != nil {
		return "", fmt.Errorf("container create %s: %w", opts.ContainerName, err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, dockercontainer.StartOptions{}); err != nil {
		return "", fmt.Errorf("container start %s: %w", opts.ContainerName, err)
	}

	slog.Info("started AE container",
		"name", opts.ContainerName,
		"container_id", resp.ID[:12],
		"employee_id", opts.EmployeeID,
	)
	return resp.ID, nil
}

// Stop stops an AE container gracefully.
func (m *Manager) Stop(ctx context.Context, containerName string) error {
	timeout := 30
	return m.docker.ContainerStop(ctx, containerName, dockercontainer.StopOptions{
		Timeout: &timeout,
	})
}

// Remove removes a stopped AE container.
func (m *Manager) Remove(ctx context.Context, containerName string) error {
	return m.docker.ContainerRemove(ctx, containerName, dockercontainer.RemoveOptions{
		Force: true,
	})
}

// Inspect returns the current state of an AE container.
func (m *Manager) Inspect(ctx context.Context, containerName string) (*ContainerInfo, error) {
	info, err := m.docker.ContainerInspect(ctx, containerName)
	if err != nil {
		return nil, err
	}
	return &ContainerInfo{
		ContainerID: info.ID,
		Name:        strings.TrimPrefix(info.Name, "/"),
		State:       info.State.Status,
		EmployeeID:  info.Config.Labels[LabelPrefix+"employee_id"],
		CompanyID:   info.Config.Labels[LabelPrefix+"company_id"],
		Role:        info.Config.Labels[LabelPrefix+"role"],
	}, nil
}

// ListByCompany returns all rally-managed containers for a given company.
func (m *Manager) ListByCompany(ctx context.Context, companyID string) ([]ContainerInfo, error) {
	containers, err := m.docker.ContainerList(ctx, dockercontainer.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelPrefix+"company_id="+companyID),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	result := make([]ContainerInfo, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		result = append(result, ContainerInfo{
			ContainerID: c.ID,
			Name:        name,
			State:       c.State,
			EmployeeID:  c.Labels[LabelPrefix+"employee_id"],
			CompanyID:   c.Labels[LabelPrefix+"company_id"],
			Role:        c.Labels[LabelPrefix+"role"],
		})
	}
	return result, nil
}

// Logs streams the last N lines of logs from a container.
func (m *Manager) Logs(ctx context.Context, containerName string, tail string) (io.ReadCloser, error) {
	return m.docker.ContainerLogs(ctx, containerName, dockercontainer.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
	})
}

// IsRunning returns true if the named container exists and is in running state.
func (m *Manager) IsRunning(ctx context.Context, containerName string) bool {
	info, err := m.Inspect(ctx, containerName)
	if err != nil {
		return false
	}
	return info.State == "running"
}

// Restart stops and starts a container.
func (m *Manager) Restart(ctx context.Context, containerName string) error {
	timeout := 30
	return m.docker.ContainerRestart(ctx, containerName, dockercontainer.StopOptions{
		Timeout: &timeout,
	})
}
