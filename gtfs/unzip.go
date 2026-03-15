package gtfs

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxExtractedBytes = 1 << 30
	maxExtractFiles   = 1000
)

func Unzip(src string, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if len(r.File) > maxExtractFiles {
		return fmt.Errorf("zip contains %d files, exceeds maximum of %d", len(r.File), maxExtractFiles)
	}

	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	var totalBytes int64

	for _, f := range r.File {
		destPath := filepath.Join(dest, f.Name)

		if !strings.HasPrefix(filepath.Clean(destPath)+string(os.PathSeparator), cleanDest) {
			return fmt.Errorf("zip entry %q would escape destination directory", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0750); err != nil {
				return fmt.Errorf("create directory %q: %w", destPath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
			return fmt.Errorf("create parent directory for %q: %w", destPath, err)
		}

		n, err := extractFile(f, destPath)
		if err != nil {
			return err
		}
		totalBytes += n
		if totalBytes > maxExtractedBytes {
			return fmt.Errorf("zip extraction exceeded %d bytes limit (possible zip bomb)", maxExtractedBytes)
		}
	}

	return nil
}

func extractFile(f *zip.File, destPath string) (int64, error) {
	outFile, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", destPath, err)
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	n, err := io.Copy(outFile, rc)
	if err != nil {
		return n, fmt.Errorf("extract %q: %w", f.Name, err)
	}
	return n, nil
}
