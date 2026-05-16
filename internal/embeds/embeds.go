package embeds

import (
	"embed"
	"io"
	"os"
	"path/filepath"
)

//go:embed all:third_party
var Data embed.FS

func ExtractAll(targetDir string) error {
	return extractDir("third_party", targetDir)
}

func extractDir(path string, targetDir string) error {
	entries, err := Data.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(path, entry.Name())
		dstPath := filepath.Join(targetDir, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := extractDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := extractFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractFile(srcPath string, dstPath string) error {
	if _, err := os.Stat(dstPath); err == nil {
		// File already exists, skip or overwrite?
		// For standalone, we usually overwrite to ensure latest version.
	}

	srcFile, err := Data.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
