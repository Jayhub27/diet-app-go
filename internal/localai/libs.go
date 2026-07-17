package localai

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// libFilename returns the llama.cpp shared library name for the given OS.
func libFilename(goos string) string {
	switch goos {
	case "linux":
		return "libllama.so"
	case "windows":
		return "llama.dll"
	case "darwin":
		return "libllama.dylib"
	default:
		return "libllama.so"
	}
}

// libsAlreadyInstalled checks whether the llama.cpp library is present at libPath.
func libsAlreadyInstalled(libPath string) bool {
	_, err := os.Stat(filepath.Join(libPath, libFilename(runtime.GOOS)))
	return err == nil
}

// hasCUDA runs nvidia-smi and parses the CUDA version. Returns false on macOS
// or when nvidia-smi is unavailable.
func hasCUDA() (bool, string) {
	if runtime.GOOS == "darwin" {
		return false, ""
	}
	out, err := exec.Command("nvidia-smi").CombinedOutput()
	if err != nil {
		return false, ""
	}
	re := regexp.MustCompile(`CUDA Version:\s*([0-9.]+)`)
	m := re.FindStringSubmatch(string(out))
	if len(m) >= 2 {
		return true, m[1]
	}
	return true, ""
}

// hasROCm runs rocminfo and parses the ROCm version. Returns false on non-Linux
// or when rocminfo is unavailable.
func hasROCm() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, ""
	}
	out, err := exec.Command("rocminfo").CombinedOutput()
	if err != nil {
		return false, ""
	}
	re := regexp.MustCompile(`Runtime Version:\s*([0-9.]+)`)
	m := re.FindStringSubmatch(string(out))
	if len(m) >= 2 {
		return true, m[1]
	}
	return true, ""
}

// versionURL is the JSON endpoint that reports the latest llama.cpp build tag.
const versionURL = "https://hybridgroup.github.io/llama-cpp-builder/version.json"

// previousVersionURL is a fallback when the latest build is still in progress.
const previousVersionURL = "https://hybridgroup.github.io/llama-cpp-builder/previous.json"

type versionTag struct {
	TagName string `json:"tag_name"`
}

func fetchVersion(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d from %s", resp.StatusCode, url)
	}
	var tag versionTag
	if err := json.NewDecoder(resp.Body).Decode(&tag); err != nil {
		return "", err
	}
	if !strings.HasPrefix(tag.TagName, "b") {
		return "", fmt.Errorf("invalid version %q", tag.TagName)
	}
	return tag.TagName, nil
}

// downloadLibs downloads and extracts prebuilt llama.cpp binaries for the
// current platform into dest. It mirrors the behaviour of
// yzma/pkg/download.Get without the go-getter / cloud-SDK dependency tree.
func downloadLibs(arch, goos, processor, version, dest string) error {
	autoVersion := false
	if version == "" || version == "latest" {
		autoVersion = true
		v, err := fetchVersion(versionURL)
		if err != nil {
			return fmt.Errorf("fetch latest version: %w", err)
		}
		version = v
	}

	url, err := libDownloadURL(arch, goos, processor, version, dest)
	if err != nil {
		return err
	}

	if err := downloadAndExtract(context.Background(), url, dest); err != nil {
		if autoVersion && strings.Contains(err.Error(), "404") {
			prev, perr := fetchVersion(previousVersionURL)
			if perr != nil {
				return err
			}
			url, err = libDownloadURL(arch, goos, processor, prev, dest)
			if err != nil {
				return err
			}
			return downloadAndExtract(context.Background(), url, dest)
		}
		return err
	}
	return nil
}

// libDownloadURL builds the GitHub release download URL + filename for the
// given platform parameters, returning the full URL. For Windows CUDA it
// also downloads the CUDA runtime zip first.
func libDownloadURL(arch, goos, processor, version, dest string) (string, error) {
	location := fmt.Sprintf("https://github.com/ggml-org/llama.cpp/releases/download/%s", version)

	switch goos {
	case "linux", "bookworm", "trixie":
		switch processor {
		case "cpu":
			if arch == "arm64" {
				return fmt.Sprintf("%s/%s",
					fmt.Sprintf("https://github.com/hybridgroup/llama-cpp-builder/releases/download/%s", version),
					fmt.Sprintf("llama-%s-bin-ubuntu-cpu-arm64.tar.gz", version)), nil
			}
			return fmt.Sprintf("%s/llama-%s-bin-ubuntu-x64.tar.gz", location, version), nil
		case "cuda":
			builder := fmt.Sprintf("https://github.com/hybridgroup/llama-cpp-builder/releases/download/%s", version)
			if arch == "arm64" {
				return fmt.Sprintf("%s/llama-%s-bin-ubuntu-cuda-arm64.tar.gz", builder, version), nil
			}
			return fmt.Sprintf("%s/llama-%s-bin-ubuntu-cuda-13-x64.tar.gz", builder, version), nil
		case "vulkan":
			if arch == "arm64" {
				builder := fmt.Sprintf("https://github.com/hybridgroup/llama-cpp-builder/releases/download/%s", version)
				return fmt.Sprintf("%s/llama-%s-bin-ubuntu-vulkan-arm64.tar.gz", builder, version), nil
			}
			return fmt.Sprintf("%s/llama-%s-bin-ubuntu-vulkan-x64.tar.gz", location, version), nil
		case "rocm":
			if arch != "amd64" {
				return "", fmt.Errorf("ROCm prebuilt binaries only available for amd64")
			}
			return fmt.Sprintf("%s/llama-%s-bin-ubuntu-rocm-7.2-x64.tar.gz", location, version), nil
		}
	case "darwin":
		switch processor {
		case "metal", "cpu":
			if arch == "arm64" {
				return fmt.Sprintf("%s/llama-%s-bin-macos-arm64.tar.gz", location, version), nil
			}
			return fmt.Sprintf("%s/llama-%s-bin-macos-x64.tar.gz", location, version), nil
		}
	case "windows":
		switch processor {
		case "cpu":
			if arch == "arm64" {
				return fmt.Sprintf("%s/llama-%s-bin-win-cpu-arm64.zip", location, version), nil
			}
			return fmt.Sprintf("%s/llama-%s-bin-win-cpu-x64.zip", location, version), nil
		case "vulkan":
			if arch == "arm64" {
				return "", fmt.Errorf("Windows ARM64 Vulkan prebuilt binaries not available")
			}
			return fmt.Sprintf("%s/llama-%s-bin-win-vulkan-x64.zip", location, version), nil
		case "rocm":
			if arch != "amd64" {
				return "", fmt.Errorf("Windows ARM64 ROCm prebuilt binaries not available")
			}
			return fmt.Sprintf("%s/llama-%s-bin-win-hip-radeon-x64.zip", location, version), nil
		case "cuda":
			if arch == "arm64" {
				return "", fmt.Errorf("Windows ARM64 CUDA prebuilt binaries not available")
			}
			cudartURL := fmt.Sprintf("%s/cudart-llama-bin-win-cuda-13.1-x64.zip", location)
			if err := downloadAndExtract(context.Background(), cudartURL, dest); err != nil {
				return "", fmt.Errorf("download CUDA runtime: %w", err)
			}
			return fmt.Sprintf("%s/llama-%s-bin-win-cuda-13.1-x64.zip", location, version), nil
		}
	}
	return "", fmt.Errorf("unsupported os=%s processor=%s arch=%s", goos, processor, arch)
}

// downloadAndExtract fetches a .tar.gz or .zip from url and extracts its
// contents into dest, stripping the top-level directory.
func downloadAndExtract(ctx context.Context, url, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "diet-app-go")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("404 not found: %s", url)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	switch {
	case strings.HasSuffix(url, ".tar.gz"):
		return extractTarGz(bytes.NewReader(body), dest)
	case strings.HasSuffix(url, ".zip"):
		return extractZip(bytes.NewReader(body), dest)
	default:
		return fmt.Errorf("unknown archive format for %s", url)
	}
}

func extractTarGz(r io.Reader, dest string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar header: %w", err)
		}

		name := header.Name
		if idx := strings.Index(name, "/"); idx != -1 {
			name = name[idx+1:]
		}
		if name == "" {
			continue
		}

		target := filepath.Join(dest, filepath.Clean(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	return nil
}

func extractZip(r *bytes.Reader, dest string) error {
	zr, err := zip.NewReader(r, r.Size())
	if err != nil {
		return fmt.Errorf("zip reader: %w", err)
	}
	for _, f := range zr.File {
		name := f.Name
		if idx := strings.Index(name, "/"); idx != -1 {
			name = name[idx+1:]
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.Clean(name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		out.Close()
	}
	return nil
}
