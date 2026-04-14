// Package vm implements the VM lifecycle: create, start, stop, delete, ssh.
package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cep-platform/vm-builder/internal/arch"
	"github.com/cep-platform/vm-builder/internal/cloudinit"
	"github.com/cep-platform/vm-builder/internal/config"
	"github.com/cep-platform/vm-builder/internal/image"
	"github.com/cep-platform/vm-builder/internal/qemu"
)

// CreateOptions holds the user-facing options for creating a new VM.
type CreateOptions struct {
	Name          string
	MemoryMB      int
	DiskGB        int
	CPUs          int
	SSHKeyPath    string
	BridgeIface   string
	Bridged       bool
	SSHPort       int
	AlpineVersion string
}

// Create provisions a new VM: downloads the base image, clones and resizes
// the disk, generates a cloud-init seed ISO, and persists the config.
func Create(opts CreateOptions) (*config.VMConfig, error) {
	a := arch.Host()

	if opts.AlpineVersion == "" {
		opts.AlpineVersion = config.DefaultAlpineVersion
	}
	if opts.MemoryMB == 0 {
		opts.MemoryMB = config.DefaultMemoryMB
	}
	if opts.DiskGB == 0 {
		opts.DiskGB = config.DefaultDiskGB
	}
	if opts.CPUs == 0 {
		opts.CPUs = config.DefaultCPUs
	}
	if opts.SSHPort == 0 {
		opts.SSHPort = 2222
	}

	// Resolve SSH public key
	sshPubKey, err := readSSHPubKey(opts.SSHKeyPath)
	if err != nil {
		return nil, fmt.Errorf("SSH key: %w", err)
	}

	// Create VM directory
	vmDir, err := config.VMDir(opts.Name)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(vmDir); statErr == nil {
		return nil, fmt.Errorf("VM %q already exists", opts.Name)
	}
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, err
	}

	// Download (or use cached) base image
	baseImage, err := image.EnsureBaseImage(a, opts.AlpineVersion)
	if err != nil {
		return nil, err
	}

	// Clone base image and resize to requested disk size
	diskPath := filepath.Join(vmDir, "disk.qcow2")
	fmt.Printf("Creating disk image (%dGB)...\n", opts.DiskGB)
	if err := cloneDisk(baseImage, diskPath, opts.DiskGB); err != nil {
		return nil, err
	}

	// Generate cloud-init seed ISO
	ciPath := filepath.Join(vmDir, "cloud-init.iso")
	fmt.Println("Generating cloud-init seed...")
	ciCfg := cloudinit.Config{
		Hostname:  opts.Name,
		SSHPubKey: sshPubKey,
	}
	if err := cloudinit.Generate(ciCfg, ciPath); err != nil {
		return nil, fmt.Errorf("cloud-init: %w", err)
	}

	cfg := &config.VMConfig{
		Name:          opts.Name,
		Arch:          string(a),
		MemoryMB:      opts.MemoryMB,
		DiskGB:        opts.DiskGB,
		CPUs:          opts.CPUs,
		SSHKeyPath:    opts.SSHKeyPath,
		SSHUser:       config.DefaultSSHUser,
		BridgeIface:   opts.BridgeIface,
		SSHPort:       opts.SSHPort,
		Bridged:       opts.Bridged,
		AlpineVersion: opts.AlpineVersion,
		CreatedAt:     time.Now(),
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}

	fmt.Printf("\nVM %q created successfully.\n", opts.Name)
	if opts.Bridged {
		fmt.Println("Networking: bridged (VM will get a LAN IP via DHCP)")
		fmt.Println("Tip: run `vmbuilder start` then check your router/ARP table for the VM's IP.")
	} else {
		fmt.Printf("Networking: user-mode, SSH forwarded to localhost:%d\n", opts.SSHPort)
		fmt.Printf("Connect with: ssh -p %d %s@localhost\n", opts.SSHPort, config.DefaultSSHUser)
	}
	return cfg, nil
}

// Start launches a created VM.
func Start(name string) error {
	cfg, err := config.Load(name)
	if err != nil {
		return err
	}

	vmDir, err := config.VMDir(name)
	if err != nil {
		return err
	}
	diskPath := filepath.Join(vmDir, "disk.qcow2")
	ciPath := filepath.Join(vmDir, "cloud-init.iso")

	// Check cloud-init ISO; it should be removed after first boot but we keep a
	// copy so the VM can be started again safely (cloud-init is idempotent).
	if _, err := os.Stat(ciPath); os.IsNotExist(err) {
		return fmt.Errorf("cloud-init ISO missing — VM may be corrupt, try re-creating")
	}

	fmt.Printf("Starting VM %q...\n", name)
	cmd, err := qemu.Start(cfg, diskPath, ciPath)
	if err != nil {
		return err
	}

	if err := config.SaveState(name, config.StateFile{PID: cmd.Process.Pid}); err != nil {
		return err
	}

	fmt.Printf("VM started (PID %d)\n", cmd.Process.Pid)
	printConnectInfo(cfg)
	return nil
}

// Stop gracefully shuts down a running VM.
func Stop(name string) error {
	state, err := config.LoadState(name)
	if err != nil {
		return err
	}
	if state.PID == 0 {
		return fmt.Errorf("VM %q is not running (no PID)", name)
	}

	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", state.PID, err)
	}

	fmt.Printf("Stopping VM %q (PID %d)...\n", name, state.PID)

	// Send SIGTERM first to allow QEMU to do a clean shutdown
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Already dead? Clear state and return
		_ = config.ClearState(name)
		return fmt.Errorf("signal: %w", err)
	}

	// Wait up to 15 seconds for clean exit, then SIGKILL
	done := make(chan error, 1)
	go func() { _, err := proc.Wait(); done <- err }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		fmt.Println("VM did not stop gracefully; force-killing...")
		_ = proc.Kill()
	}

	_ = config.ClearState(name)
	fmt.Printf("VM %q stopped.\n", name)
	return nil
}

// Delete removes a VM and all its data.
func Delete(name string, force bool) error {
	if !force {
		state, _ := config.LoadState(name)
		if state != nil && state.PID != 0 {
			return fmt.Errorf("VM %q is running; stop it first or use --force", name)
		}
	}
	vmDir, err := config.VMDir(name)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(vmDir); os.IsNotExist(statErr) {
		return fmt.Errorf("VM %q not found", name)
	}
	if err := os.RemoveAll(vmDir); err != nil {
		return err
	}
	fmt.Printf("VM %q deleted.\n", name)
	return nil
}

// SSH opens an interactive SSH session to the VM.
func SSH(name string, extraArgs []string) error {
	cfg, err := config.Load(name)
	if err != nil {
		return err
	}

	state, err := config.LoadState(name)
	if err != nil {
		return err
	}

	var sshArgs []string

	// Use stored IP for bridged, or localhost + port forward for user-mode
	if cfg.Bridged && state.IP != "" {
		sshArgs = append(sshArgs,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		)
		if cfg.SSHKeyPath != "" {
			sshArgs = append(sshArgs, "-i", cfg.SSHKeyPath)
		}
		sshArgs = append(sshArgs, cfg.SSHUser+"@"+state.IP)
	} else {
		port := cfg.SSHPort
		if port == 0 {
			port = 2222
		}
		sshArgs = append(sshArgs,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-p", strconv.Itoa(port),
		)
		if cfg.SSHKeyPath != "" {
			sshArgs = append(sshArgs, "-i", cfg.SSHKeyPath)
		}
		sshArgs = append(sshArgs, cfg.SSHUser+"@localhost")
	}

	sshArgs = append(sshArgs, extraArgs...)

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH")
	}

	cmd := exec.Command(sshBin, sshArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Status prints a human-readable summary of all VMs.
func Status() error {
	names, err := config.ListVMs()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No VMs found. Create one with: vmbuilder create <name>")
		return nil
	}

	fmt.Printf("%-20s %-10s %-8s %-8s %-8s %-16s %s\n",
		"NAME", "STATE", "CPUS", "RAM(MB)", "DISK(GB)", "IP/PORT", "ARCH")
	fmt.Println(strings.Repeat("-", 85))

	for _, name := range names {
		cfg, err := config.Load(name)
		if err != nil {
			fmt.Printf("%-20s [error loading config: %v]\n", name, err)
			continue
		}
		state, _ := config.LoadState(name)

		vmState := "stopped"
		ipPort := "-"

		if state != nil && state.PID != 0 {
			if isRunning(state.PID) {
				vmState = "running"
				if cfg.Bridged && state.IP != "" {
					ipPort = state.IP
				} else if !cfg.Bridged {
					ipPort = fmt.Sprintf("localhost:%d", cfg.SSHPort)
				}
			}
		}

		fmt.Printf("%-20s %-10s %-8d %-8d %-8d %-16s %s\n",
			name, vmState, cfg.CPUs, cfg.MemoryMB, cfg.DiskGB, ipPort, cfg.Arch)
	}
	return nil
}

// isRunning checks if a process with the given PID is alive.
func isRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func cloneDisk(src, dest string, sizeGB int) error {
	qimgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("qemu-img not found — install QEMU:\n  macOS: brew install qemu\n  Linux: apt install qemu-utils")
	}

	// Create a QCOW2 overlay that uses the base image as a backing file.
	// This avoids copying the full image and is space-efficient.
	out, err := exec.Command(qimgPath, "create",
		"-f", "qcow2",
		"-b", src,
		"-F", "qcow2",
		dest,
		fmt.Sprintf("%dG", sizeGB),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create: %w\n%s", err, out)
	}

	// Resize the virtual disk
	out, err = exec.Command(qimgPath, "resize", dest, fmt.Sprintf("%dG", sizeGB)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img resize: %w\n%s", err, out)
	}
	return nil
}

func readSSHPubKey(keyPath string) (string, error) {
	if keyPath == "" {
		// Try common defaults
		home, _ := os.UserHomeDir()
		candidates := []string{
			filepath.Join(home, ".ssh", "id_ed25519.pub"),
			filepath.Join(home, ".ssh", "id_rsa.pub"),
			filepath.Join(home, ".ssh", "id_ecdsa.pub"),
		}
		for _, p := range candidates {
			if data, err := os.ReadFile(p); err == nil {
				fmt.Printf("Using SSH key: %s\n", p)
				return strings.TrimSpace(string(data)), nil
			}
		}
		return "", fmt.Errorf("no SSH public key found; specify one with --ssh-key")
	}

	// If a private key path was given, try the .pub counterpart
	pubPath := keyPath
	if !strings.HasSuffix(pubPath, ".pub") {
		pubPath = keyPath + ".pub"
	}
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", pubPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func printConnectInfo(cfg *config.VMConfig) {
	fmt.Println("\nFirst boot takes ~60-90 seconds (packages install via cloud-init).")
	if cfg.Bridged {
		fmt.Println("VM will get a DHCP address on your LAN. Check your router's client list.")
		fmt.Println("Once you have the IP: ssh alpine@<ip>")
	} else {
		fmt.Printf("SSH available at: ssh -p %d %s@localhost\n", cfg.SSHPort, cfg.SSHUser)
		fmt.Printf("Or use: vmbuilder ssh %s\n", cfg.Name)
	}
	fmt.Printf("\nSerial console log: ~/.vmbuilder/vms/%s/serial.log\n", cfg.Name)
}
