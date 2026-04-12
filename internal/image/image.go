package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/adam/vm-builder/internal/arch"
	"github.com/adam/vm-builder/internal/config"
)

const alpineCDN = "https://dl-cdn.alpinelinux.org/alpine"

// EnsureBaseImage downloads the Alpine cloud image if not already cached.
// Returns the path to the cached base image.
func EnsureBaseImage(a arch.Arch, alpineVersion string) (string, error) {
	cacheDir, err := config.ImageCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}

	imageName := a.AlpineImageName(alpineVersion)
	imagePath := filepath.Join(cacheDir, imageName)

	if _, err := os.Stat(imagePath); err == nil {
		fmt.Printf("Using cached image: %s\n", imageName)
		return imagePath, nil
	}

	// Parse major.minor version for CDN URL path (e.g., "3.21.6" -> "v3.21")
	parts := strings.Split(alpineVersion, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid alpine version %q", alpineVersion)
	}
	majorMinor := fmt.Sprintf("v%s.%s", parts[0], parts[1])

	baseURL := fmt.Sprintf("%s/%s/releases/cloud", alpineCDN, majorMinor)
	imageURL := fmt.Sprintf("%s/%s", baseURL, imageName)
	sha256URL := fmt.Sprintf("%s/SHA256SUMS", baseURL)

	fmt.Printf("Downloading Alpine Linux %s (%s)...\n", alpineVersion, a.AlpineArch())
	fmt.Printf("  URL: %s\n", imageURL)

	tmpPath := imagePath + ".tmp"
	if err := downloadFile(tmpPath, imageURL); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("download failed: %w", err)
	}

	if err := verifySHA256(tmpPath, imageName, sha256URL); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}

	if err := os.Rename(tmpPath, imagePath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	fmt.Printf("Image downloaded and verified: %s\n", imagePath)
	return imagePath, nil
}

func downloadFile(dest, url string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	downloaded := int64(0)
	lastPct := -1

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			downloaded += int64(n)
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct != lastPct && pct%10 == 0 {
					fmt.Printf("  %d%%\n", pct)
					lastPct = pct
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func verifySHA256(filePath, imageName, sha256URL string) error {
	resp, err := http.Get(sha256URL) //nolint:gosec
	if err != nil {
		fmt.Printf("Warning: could not fetch SHA256SUMS, skipping verification: %v\n", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Warning: SHA256SUMS returned HTTP %d, skipping verification\n", resp.StatusCode)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	expectedHash := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == imageName {
			expectedHash = fields[0]
			break
		}
	}

	if expectedHash == "" {
		fmt.Printf("Warning: checksum not found for %s, skipping verification\n", imageName)
		return nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if actualHash != expectedHash {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	fmt.Println("  Checksum verified ✓")
	return nil
}
