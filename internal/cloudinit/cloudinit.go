// Package cloudinit generates the cloud-init NoCloud seed ISO used for
// first-boot VM configuration. The ISO (labelled "cidata") carries two files:
// meta-data and user-data.
//
// On macOS the seed is built with hdiutil; on Linux with genisoimage or
// xorriso (whichever is found first on PATH).
package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Config holds the parameters used to render the cloud-init user-data.
type Config struct {
	Hostname   string
	SSHPubKey  string // full public key line, e.g. "ssh-ed25519 AAAA..."
	ExtraSetup []string
	Arch       string // "amd64" or "arm64" — used to fetch the correct Go tarball
	GoVersion  string // Go version to install, e.g. "1.25.5"
}

// Generate creates a cloud-init seed ISO at destPath.
func Generate(cfg Config, destPath string) error {
	tmpDir, err := os.MkdirTemp("", "cidata-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "meta-data"), []byte(metaData(cfg.Hostname)), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "user-data"), []byte(userData(cfg)), 0644); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "darwin":
		return buildWithHdiutil(tmpDir, destPath)
	default:
		return buildWithGenisoimageOrXorriso(tmpDir, destPath)
	}
}

func metaData(hostname string) string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", hostname, hostname)
}

func userData(cfg Config) string {
	var sb strings.Builder
	sb.WriteString("#cloud-config\n")

	if cfg.SSHPubKey != "" {
		sb.WriteString("ssh_authorized_keys:\n")
		sb.WriteString(fmt.Sprintf("  - %s\n", strings.TrimSpace(cfg.SSHPubKey)))
	}

	sb.WriteString("\n# Disable password auth, require SSH key\n")
	sb.WriteString("ssh_pwauth: false\n\n")

	// Install required packages via apk (Alpine).
	// cloud-init's 'packages' module calls the native package manager.
	sb.WriteString("packages:\n")
	sb.WriteString("  - git\n")
	sb.WriteString("  - openssh\n")
	sb.WriteString("  - curl\n")
	sb.WriteString("  - bash\n")

	// Write a reusable script that installs the native build/graphics deps needed
	// by Go packages that link against OpenGL, X11, etc.
	sb.WriteString("\nwrite_files:\n")
	sb.WriteString("  - path: /usr/local/bin/install-go-deps\n")
	sb.WriteString("    permissions: '0755'\n")
	sb.WriteString("    content: |\n")
	sb.WriteString("      #!/bin/sh\n")
	sb.WriteString("      apk add \\\n")
	sb.WriteString("        gcc musl-dev \\\n")
	sb.WriteString("        mesa-dev mesa-gl mesa-egl mesa-gles \\\n")
	sb.WriteString("        libx11-dev libxcursor-dev libxrandr-dev \\\n")
	sb.WriteString("        libxinerama-dev libxi-dev libxxf86vm-dev \\\n")
	sb.WriteString("        pkgconfig\n")

	// Install Go from the official tarball to guarantee the required version.
	goURL := fmt.Sprintf("https://go.dev/dl/go%s.linux-%s.tar.gz", cfg.GoVersion, cfg.Arch)
	sb.WriteString("\nruncmd:\n")
	sb.WriteString(fmt.Sprintf("  - curl -fsSL %s | tar -C /usr/local -xz\n", goURL))
	sb.WriteString("  - /usr/local/bin/install-go-deps\n")
	sb.WriteString("  - rc-update add sshd default\n")
	sb.WriteString("  - rc-service sshd start\n")
	// Persist sshd across reboots (Alpine uses OpenRC)
	sb.WriteString("  - echo 'sshd:ALL' >> /etc/hosts.allow\n")
	// Set GOPATH / PATH in profile
	sb.WriteString("  - echo 'export GOPATH=/root/go' >> /etc/profile\n")
	sb.WriteString("  - echo 'export PATH=$PATH:/root/go/bin:/usr/local/go/bin' >> /etc/profile\n")

	// Any extra setup commands provided by the caller
	for _, cmd := range cfg.ExtraSetup {
		sb.WriteString(fmt.Sprintf("  - %s\n", cmd))
	}

	sb.WriteString("\nfinal_message: |\n")
	sb.WriteString("  vmbuilder setup complete. Connect with: ssh alpine@<ip>\n")

	return sb.String()
}

func buildWithHdiutil(srcDir, destPath string) error {
	// hdiutil makehybrid produces an ISO that cloud-init can read.
	// The -default-volume-name sets the ISO label to "cidata" as required by NoCloud.
	cmd := exec.Command("hdiutil", "makehybrid",
		"-o", destPath,
		"-hfs",
		"-joliet",
		"-iso",
		"-default-volume-name", "cidata",
		srcDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil: %w\n%s", err, out)
	}
	return nil
}

func buildWithGenisoimageOrXorriso(srcDir, destPath string) error {
	metaFile := filepath.Join(srcDir, "meta-data")
	userFile := filepath.Join(srcDir, "user-data")

	// Try genisoimage first, then xorriso (as mkisofs compatibility shim).
	for _, tool := range []string{"genisoimage", "xorriso", "mkisofs"} {
		toolPath, err := exec.LookPath(tool)
		if err != nil {
			continue
		}

		var cmd *exec.Cmd
		switch tool {
		case "xorriso":
			cmd = exec.Command(toolPath,
				"-as", "mkisofs",
				"-output", destPath,
				"-volid", "cidata",
				"-joliet",
				"-rock",
				metaFile, userFile,
			)
		default:
			cmd = exec.Command(toolPath,
				"-output", destPath,
				"-volid", "cidata",
				"-joliet",
				"-rock",
				metaFile, userFile,
			)
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w\n%s", tool, err, out)
		}
		return nil
	}

	return fmt.Errorf("no ISO creation tool found; install genisoimage or xorriso")
}
