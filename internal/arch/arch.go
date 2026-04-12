package arch

import (
"fmt"
"os"
"runtime"
)

type Arch string

const (
ARM64 Arch = "arm64"
AMD64 Arch = "amd64"
)

func Host() Arch {
switch runtime.GOARCH {
case "arm64":
return ARM64
default:
return AMD64
}
}

func HostOS() string {
return runtime.GOOS
}

func (a Arch) QEMUBinary() string {
switch a {
case ARM64:
return "qemu-system-aarch64"
default:
return "qemu-system-x86_64"
}
}

func (a Arch) AlpineArch() string {
switch a {
case ARM64:
return "aarch64"
default:
return "x86_64"
}
}

// AlpineImageName returns the cloud image filename for this architecture.
// aarch64 uses UEFI, x86_64 uses BIOS.
func (a Arch) AlpineImageName(version string) string {
switch a {
case ARM64:
return fmt.Sprintf("nocloud_alpine-%s-aarch64-uefi-cloudinit-r0.qcow2", version)
default:
return fmt.Sprintf("nocloud_alpine-%s-x86_64-bios-cloudinit-r0.qcow2", version)
}
}

// EFIFirmware returns the path to the UEFI firmware. Required for ARM64.
// Returns empty string for x86_64 (uses built-in BIOS).
func (a Arch) EFIFirmware() string {
if a != ARM64 {
return ""
}
candidates := []string{
"/opt/homebrew/share/qemu/edk2-aarch64-code.fd", // Homebrew macOS Apple Silicon
"/usr/share/qemu/edk2-aarch64-code.fd",
"/usr/share/AAVMF/AAVMF_CODE.fd",
"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
}
for _, p := range candidates {
if _, err := os.Stat(p); err == nil {
return p
}
}
return ""
}
