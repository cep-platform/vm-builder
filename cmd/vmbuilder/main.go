package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/adam/vm-builder/internal/vm"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "vmbuilder",
		Short: "Spin up lightweight Alpine Linux VMs with QEMU",
		Long: `vmbuilder creates and manages QEMU-backed Alpine Linux VMs.

VMs are stored in ~/.vmbuilder/vms/<name>/.
Base images are cached in ~/.vmbuilder/images/.

Hardware acceleration is used automatically:
  macOS  → HVF (Apple Hypervisor Framework)
  Linux  → KVM

Requirements:
  macOS: brew install qemu
  Linux: apt install qemu-system-arm qemu-utils qemu-efi-aarch64  (ARM64)
         apt install qemu-system-x86 qemu-utils                    (x86_64)

For bridged networking on macOS, QEMU must be run as root or the binary
must carry the com.apple.vm.networking entitlement (Homebrew QEMU does NOT
ship with it by default). See README for alternatives (socket_vmnet).`,
	}

	root.AddCommand(
		createCmd(),
		startCmd(),
		stopCmd(),
		deleteCmd(),
		sshCmd(),
		listCmd(),
	)
	return root
}

func createCmd() *cobra.Command {
	opts := vm.CreateOptions{}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Alpine Linux VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			_, err := vm.Create(opts)
			return err
		},
	}

	cmd.Flags().IntVar(&opts.MemoryMB, "ram", 4096, "RAM in megabytes")
	cmd.Flags().IntVar(&opts.DiskGB, "disk", 20, "Disk size in gigabytes")
	cmd.Flags().IntVar(&opts.CPUs, "cpus", 2, "Number of virtual CPUs")
	cmd.Flags().StringVar(&opts.SSHKeyPath, "ssh-key", "", "Path to SSH private or public key (auto-detected if omitted)")
	cmd.Flags().StringVar(&opts.BridgeIface, "bridge", "", "Host network interface for bridged mode (e.g. en0, eth0)")
	cmd.Flags().BoolVar(&opts.Bridged, "bridged", false, "Use bridged networking (VM gets its own LAN IP)")
	cmd.Flags().IntVar(&opts.SSHPort, "ssh-port", 2222, "Host port to forward to VM's SSH (user-mode networking only)")
	cmd.Flags().StringVar(&opts.AlpineVersion, "alpine-version", "3.21.6", "Alpine Linux version")

	return cmd
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <name>",
		Short: "Start a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vm.Start(args[0])
		},
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a running VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vm.Stop(args[0])
		},
	}
}

func deleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a VM and all its data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return vm.Delete(args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force delete even if VM is running")
	return cmd
}

func sshCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "ssh <name> [-- ssh-args...]",
		Short:              "Open an SSH session to the VM",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("VM name required")
			}
			return vm.SSH(args[0], args[1:])
		},
	}
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all VMs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return vm.Status()
		},
	}
}
