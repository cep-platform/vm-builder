// Package qemu builds and manages QEMU processes.
package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/cep-platform/vm-builder/internal/arch"
	"github.com/cep-platform/vm-builder/internal/config"
)

// Args builds the full qemu-system-* argument list for launching a VM.
func Args(cfg *config.VMConfig, diskPath, cloudInitISO string) ([]string, error) {
	a := arch.Arch(cfg.Arch)
	firmware := a.EFIFirmware()

	if a == arch.ARM64 && firmware == "" {
		return nil, fmt.Errorf(
			"UEFI firmware not found for aarch64.\n" +
				"On macOS: brew install qemu\n" +
				"On Linux: apt install qemu-efi-aarch64  OR  dnf install edk2-aarch64",
		)
	}

	args := []string{}

	// Machine type and acceleration
	switch a {
	case arch.ARM64:
		args = append(args, "-machine", "virt")
		args = append(args, "-cpu", "host")
		args = append(args, "-bios", firmware)
	default:
		args = append(args, "-machine", "q35")
		args = append(args, "-cpu", "host")
	}

	// Hardware acceleration
	accel := accelFlag()
	if accel != "" {
		args = append(args, "-accel", accel)
	}

	// CPU and memory
	args = append(args, "-smp", strconv.Itoa(cfg.CPUs))
	args = append(args, "-m", strconv.Itoa(cfg.MemoryMB))

	// Main disk
	args = append(args,
		"-drive", fmt.Sprintf("if=virtio,file=%s,format=qcow2", diskPath),
	)

	// Cloud-init seed ISO (attached as second virtio block device)
	args = append(args,
		"-drive", fmt.Sprintf("if=virtio,file=%s,format=raw,readonly=on", cloudInitISO),
	)

	// Networking
	netArgs, err := networkArgs(cfg)
	if err != nil {
		return nil, err
	}
	args = append(args, netArgs...)

	// Serial console (useful for debugging first boot)
	args = append(args, "-serial", "file:"+vmSerialLog(cfg.Name))

	// No display; headless
	args = append(args, "-display", "none")

	// PID file so we can track the process
	args = append(args, "-pidfile", vmPIDFile(cfg.Name))

	// RNG device for faster boot entropy
	args = append(args, "-device", "virtio-rng-pci")

	return args, nil
}

func accelFlag() string {
	switch runtime.GOOS {
	case "darwin":
		return "hvf"
	case "linux":
		if _, err := os.Stat("/dev/kvm"); err == nil {
			return "kvm"
		}
		return "" // no acceleration available; QEMU will warn
	default:
		return ""
	}
}

func networkArgs(cfg *config.VMConfig) ([]string, error) {
	if cfg.Bridged {
		return bridgedNetworkArgs(cfg)
	}
	return usermodeNetworkArgs(cfg)
}

// bridgedNetworkArgs produces arguments for bridged LAN networking.
//
// macOS: uses vmnet-bridged (requires the QEMU binary to carry the
// com.apple.vm.networking entitlement, or to be run as root).
//
// Linux: uses a TAP device bridged to the specified interface.
func bridgedNetworkArgs(cfg *config.VMConfig) ([]string, error) {
	iface := cfg.BridgeIface
	if iface == "" {
		iface = defaultBridgeIface()
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"-netdev", fmt.Sprintf("vmnet-bridged,id=net0,ifname=%s", iface),
			"-device", "virtio-net-pci,netdev=net0",
		}, nil
	default:
		// Linux: QEMU bridge helper or explicit tap
		return []string{
			"-netdev", fmt.Sprintf("bridge,id=net0,br=%s", iface),
			"-device", "virtio-net-pci,netdev=net0",
		}, nil
	}
}

// usermodeNetworkArgs uses QEMU's user-mode networking with a forwarded SSH port.
func usermodeNetworkArgs(cfg *config.VMConfig) ([]string, error) {
	sshPort := cfg.SSHPort
	if sshPort == 0 {
		sshPort = 2222
	}
	hostfwd := fmt.Sprintf("hostfwd=tcp::%d-:22", sshPort)
	return []string{
		"-netdev", fmt.Sprintf("user,id=net0,%s", hostfwd),
		"-device", "virtio-net-pci,netdev=net0",
	}, nil
}

func defaultBridgeIface() string {
	switch runtime.GOOS {
	case "darwin":
		return "en0"
	default:
		return "eth0"
	}
}

// Start launches QEMU for the given VM as a background process.
func Start(cfg *config.VMConfig, diskPath, cloudInitISO string) (*exec.Cmd, error) {
	a := arch.Arch(cfg.Arch)
	binary, err := exec.LookPath(a.QEMUBinary())
	if err != nil {
		return nil, fmt.Errorf("%s not found — install QEMU:\n  macOS: brew install qemu\n  Linux: apt install qemu-system-arm  OR  qemu-system-x86", a.QEMUBinary(), )
	}

	args, err := Args(cfg, diskPath, cloudInitISO)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start QEMU: %w", err)
	}
	return cmd, nil
}

func vmSerialLog(name string) string {
	dir, _ := config.VMDir(name)
	return dir + "/serial.log"
}

func vmPIDFile(name string) string {
	dir, _ := config.VMDir(name)
	return dir + "/qemu.pid"
}
