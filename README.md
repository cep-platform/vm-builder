# vmbuilder

A cross-platform CLI tool to spin up lightweight Alpine Linux VMs using QEMU.
Written in Go — runs on macOS (Apple Silicon & Intel) and Linux (arm64 & x86_64).

## Features

- **Tiny footprint** — Alpine Linux ~100 MB image; default VM: 4 GB RAM, 20 GB disk
- **Hardware acceleration** — HVF on macOS, KVM on Linux; native arch guests
- **LAN-visible VMs** — bridged networking (VM gets its own IP) or user-mode with SSH port-forwarding
- **Ready to code** — cloud-init installs `git`, `go`, `bash`, `curl` on first boot
- **Simple CLI** — `create`, `start`, `stop`, `ssh`, `list`, `delete`

## Requirements

### macOS
```bash
brew install qemu
```
QEMU brings along `qemu-img` and the UEFI firmware (`edk2-aarch64-code.fd`).

### Linux (arm64)
```bash
apt install qemu-system-aarch64 qemu-utils qemu-efi-aarch64   # Debian/Ubuntu
dnf install qemu-system-aarch64 qemu-img edk2-aarch64         # Fedora/RHEL
```

### Linux (x86_64)
```bash
apt install qemu-system-x86 qemu-utils    # Debian/Ubuntu
dnf install qemu-system-x86 qemu-img     # Fedora/RHEL
```

### Cloud-init ISO tool (Linux only — macOS uses built-in `hdiutil`)
```bash
apt install genisoimage    # or: apt install xorriso
```

## Install

```bash
go install github.com/cep-platform/vm-builder/cmd/vmbuilder@latest
```

Or build from source:
```bash
git clone https://github.com/cep-platform/vm-builder
cd vm-builder
go build -o ~/bin/vmbuilder ./cmd/vmbuilder
```

## Quick Start

```bash
# Create a VM (user-mode networking, SSH on localhost:2222)
vmbuilder create myvm

# Create with bridged networking (VM gets its own LAN IP)
vmbuilder create myvm --bridged --bridge en0

# Start the VM
vmbuilder start myvm

# Wait ~90 seconds for cloud-init to finish, then SSH in
vmbuilder ssh myvm

# Inside the VM: clone and run Go code
git clone https://github.com/your-org/hypha
cd hypha
go run .

# Stop and clean up
vmbuilder stop myvm
vmbuilder delete myvm
```

## Commands

| Command | Description |
|---------|-------------|
| `vmbuilder create <name>` | Create a new VM |
| `vmbuilder start <name>` | Start an existing VM |
| `vmbuilder stop <name>` | Gracefully stop a VM |
| `vmbuilder ssh <name>` | Open SSH session |
| `vmbuilder list` | List all VMs and status |
| `vmbuilder delete <name>` | Delete VM and all its data |

### `create` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--ram` | `4096` | RAM in MB |
| `--disk` | `20` | Disk size in GB |
| `--cpus` | `2` | vCPU count |
| `--ssh-key` | auto | Path to SSH key (auto-detects `~/.ssh/id_*.pub`) |
| `--bridged` | false | Enable bridged LAN networking |
| `--bridge` | `en0`/`eth0` | Host NIC for bridge mode |
| `--ssh-port` | `2222` | Host port forwarded to VM SSH (user-mode only) |
| `--alpine-version` | `3.21.6` | Alpine Linux release |

## Networking

### User-mode (default, no privileges needed)
SSH is forwarded from a host port to the VM:
```
ssh -p 2222 alpine@localhost
```
The VM is **not** directly visible on your LAN.

### Bridged (LAN-visible)
The VM gets its own IP address from your router's DHCP server, fully visible
on your LAN.

**macOS caveat:** vmnet-bridged requires the QEMU binary to have the
`com.apple.vm.networking` entitlement, which Apple's security model restricts.
Options:
1. **Run as root:** `sudo vmbuilder start myvm`
2. **Use socket_vmnet** (recommended for regular use):
   ```bash
   brew install socket_vmnet
   brew services start socket_vmnet
   ```
   Then modify the `BridgeIface` in `~/.vmbuilder/vms/<name>/config.json` to
   use socket_vmnet's socket path.
3. **Sign the QEMU binary** with the entitlement yourself (advanced).

**Linux:** Requires a bridge device and the `qemu-bridge-helper` with
`/etc/qemu/bridge.conf`:
```
allow br0
```

## Storage layout

```
~/.vmbuilder/
├── images/           # Cached Alpine base images (shared across VMs)
│   └── nocloud_alpine-3.21.6-aarch64-uefi-cloudinit-r0.qcow2
└── vms/
    └── myvm/
        ├── config.json       # VM configuration
        ├── disk.qcow2        # VM disk (QCOW2 overlay on base image)
        ├── cloud-init.iso    # First-boot seed
        ├── serial.log        # QEMU serial output (useful for debugging)
        └── state.json        # Runtime state (PID, IP) — present while running
```

The `disk.qcow2` is a QCOW2 overlay — it only stores the **delta** from the
base image, so multiple VMs share the base without duplicating it.

## First Boot

Cloud-init runs on first boot and installs:
- `git` — clone repositories
- `go` — run Go code
- `openssh` — SSH server
- `curl`, `bash` — utilities

Boot takes ~60–90 seconds on first launch (package download and install).
Monitor progress via the serial log:
```bash
tail -f ~/.vmbuilder/vms/myvm/serial.log
```

## Tips

```bash
# Clone and run hypha immediately after SSH
vmbuilder ssh myvm -- -t 'git clone https://github.com/your-org/hypha && cd hypha && go run .'

# Use multiple VMs on different ports
vmbuilder create dev1 --ssh-port 2222
vmbuilder create dev2 --ssh-port 2223

# Inspect QEMU args without starting
# (edit internal/qemu/qemu.go Args() and add dry-run flag as needed)
```
