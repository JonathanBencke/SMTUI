// Command packager builds a distributable .zip of the Service Manager TUI to
// share with teammates. It bundles the compiled smtui.exe, a commented example
// services.toml (generated from the same starter used on first run, so it never
// drifts), and the README/LICENSE.
//
// Usage (from the repo root, after building smtui.exe):
//
//	go run ./tools/packager
//
// Output: dist/ServiceManagerTUI.zip
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

const (
	distDir    = "dist"
	zipName    = "ServiceManagerTUI.zip"
	zipRoot    = "ServiceManagerTUI" // top-level folder inside the archive
	exeName    = "smtui.exe"
	exampleCfg = "services.example.toml"
)

// zipEntry is a file to include: srcPath on disk, and the name it gets inside
// the archive (under zipRoot). optional means a missing source is skipped.
type zipEntry struct {
	src      string
	archive  string
	optional bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "packager: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat(exeName); err != nil {
		return fmt.Errorf("%s not found — build it first (make build / go build -o %s .): %w", exeName, exeName, err)
	}

	if err := os.MkdirAll(distDir, 0755); err != nil {
		return err
	}

	// Generate the example config from the single source of truth (the starter
	// written on first run), so the bundled example never drifts from the app.
	examplePath := filepath.Join(distDir, exampleCfg)
	_ = os.Remove(examplePath)
	if err := config.WriteStarter(examplePath); err != nil {
		return fmt.Errorf("generating %s: %w", exampleCfg, err)
	}

	entries := []zipEntry{
		{src: exeName, archive: exeName},
		{src: examplePath, archive: exampleCfg},
		{src: "README.md", archive: "README.md", optional: true},
		{src: "LICENSE", archive: "LICENSE", optional: true},
	}

	zipPath := filepath.Join(distDir, zipName)
	if err := writeZip(zipPath, entries); err != nil {
		return err
	}

	info, err := os.Stat(zipPath)
	if err != nil {
		return err
	}
	fmt.Printf("Created %s (%.1f KB)\n", zipPath, float64(info.Size())/1024)
	return nil
}

func writeZip(zipPath string, entries []zipEntry) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	for _, e := range entries {
		if err := addFile(zw, e); err != nil {
			return fmt.Errorf("adding %s: %w", e.src, err)
		}
	}
	return nil
}

func addFile(zw *zip.Writer, e zipEntry) error {
	f, err := os.Open(e.src)
	if err != nil {
		if e.optional && os.IsNotExist(err) {
			fmt.Printf("skipping optional %s (not found)\n", e.src)
			return nil
		}
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(filepath.Join(zipRoot, e.archive))
	header.Method = zip.Deflate

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
