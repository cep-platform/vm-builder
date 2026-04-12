package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultAlpineVersion = "3.21.6"
	DefaultMemoryMB      = 4096
	DefaultDiskGB        = 20
	DefaultCPUs          = 2
	DefaultSSHUser       = "alpine"
	DefaultSSHPort       = 22
)

// VMState represents the runtime state of a VM.
type VMState string

const (
	StateStopped VMState = "stopped"
	StateRunning VMState = "running"
)

// VMConfig is the persisted configuration for a VM.
type VMConfig struct {
	Name         string    `json:"name"`
	Arch         string    `json:"arch"`
	MemoryMB     int       `json:"memory_mb"`
	DiskGB       int       `json:"disk_gb"`
	CPUs         int       `json:"cpus"`
	SSHKeyPath   string    `json:"ssh_key_path"`
	SSHUser      string    `json:"ssh_user"`
	BridgeIface  string    `json:"bridge_iface,omitempty"`
	SSHPort      int       `json:"ssh_port"`       // host port for SSH (user-mode networking)
	Bridged      bool      `json:"bridged"`        // true = bridged LAN, false = user-mode + port forward
	AlpineVersion string   `json:"alpine_version"`
	CreatedAt    time.Time `json:"created_at"`
}

// StateFile is written at runtime to track the running PID and IP.
type StateFile struct {
	PID int    `json:"pid"`
	IP  string `json:"ip,omitempty"`
}

// BaseDir returns the root directory for all VM data.
func BaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".vmbuilder"), nil
}

// VMDir returns the directory for a specific VM.
func VMDir(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "vms", name), nil
}

// ImageCacheDir returns the directory where base images are cached.
func ImageCacheDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "images"), nil
}

// Save writes the config to disk.
func (c *VMConfig) Save() error {
	dir, err := VMDir(c.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

// Load reads a VM config from disk.
func Load(name string) (*VMConfig, error) {
	dir, err := VMDir(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("VM %q not found", name)
		}
		return nil, err
	}
	var cfg VMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveState writes the runtime state (PID, IP) to disk.
func SaveState(name string, state StateFile) error {
	dir, err := VMDir(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0644)
}

// LoadState reads the runtime state for a VM.
func LoadState(name string) (*StateFile, error) {
	dir, err := VMDir(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return &StateFile{}, nil
		}
		return nil, err
	}
	var s StateFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ClearState removes the runtime state file.
func ClearState(name string) error {
	dir, err := VMDir(name)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "state.json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListVMs returns all known VM names.
func ListVMs() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	vmsDir := filepath.Join(base, "vms")
	entries, err := os.ReadDir(vmsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
