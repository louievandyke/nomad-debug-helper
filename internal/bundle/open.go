package bundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var bundleRootMarkers = map[string]struct{}{
	"client":        {},
	"cluster":       {},
	"interval":      {},
	"manifest.json": {},
	"metadata.json": {},
	"server":        {},
	"summary.json":  {},
}

func Open(path string) (*Bundle, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}

	if info.IsDir() {
		return OpenDirectory(absPath)
	}
	if !isSupportedArchive(absPath) {
		return nil, fmt.Errorf("%s is not a supported debug bundle directory or archive", absPath)
	}

	return OpenArchive(absPath)
}

func OpenArchive(path string) (*Bundle, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not an archive", absPath)
	}
	if !isSupportedArchive(absPath) {
		return nil, fmt.Errorf("%s is not a supported archive type", absPath)
	}

	tempDir, err := os.MkdirTemp("", "nomad-debug-helper-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	rootPath, err := extractArchive(absPath, tempDir)
	if err != nil {
		return nil, err
	}

	files, err := Inventory(rootPath)
	if err != nil {
		return nil, err
	}

	cleanup = false
	return &Bundle{
		SourcePath: absPath,
		RootPath:   rootPath,
		SourceKind: SourceArchive,
		Files:      files,
		cleanupDir: tempDir,
	}, nil
}

func OpenDirectory(path string) (*Bundle, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absPath)
	}

	rootPath, err := resolveDirectoryRoot(absPath)
	if err != nil {
		return nil, err
	}

	files, err := Inventory(rootPath)
	if err != nil {
		return nil, err
	}

	return &Bundle{
		SourcePath: absPath,
		RootPath:   rootPath,
		SourceKind: SourceDirectory,
		Files:      files,
	}, nil
}

func extractArchive(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	var tarReader *tar.Reader
	lowerPath := strings.ToLower(archivePath)

	switch {
	case strings.HasSuffix(lowerPath, ".tar.gz"), strings.HasSuffix(lowerPath, ".tgz"):
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return "", fmt.Errorf("open gzip stream: %w", err)
		}
		defer gzipReader.Close()
		tarReader = tar.NewReader(gzipReader)
	case strings.HasSuffix(lowerPath, ".tar"):
		tarReader = tar.NewReader(file)
	default:
		return "", fmt.Errorf("%s is not a supported archive type", archivePath)
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar entry: %w", err)
		}

		targetPath, err := archiveEntryPath(destDir, header.Name)
		if err != nil {
			return "", err
		}
		if targetPath == "" {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return "", fmt.Errorf("create directory %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return "", fmt.Errorf("create parent directory for %s: %w", targetPath, err)
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&os.ModePerm)
			if err != nil {
				return "", fmt.Errorf("create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return "", fmt.Errorf("extract file %s: %w", targetPath, err)
			}
			if err := outFile.Close(); err != nil {
				return "", fmt.Errorf("close file %s: %w", targetPath, err)
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		default:
			return "", fmt.Errorf("unsupported tar entry type %q for %s", string(header.Typeflag), header.Name)
		}
	}

	rootPath, err := resolveDirectoryRoot(destDir)
	if err != nil {
		return "", err
	}

	return rootPath, nil
}

func archiveEntryPath(destDir, entryName string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(entryName))
	if cleanName == "." {
		return "", nil
	}
	if filepath.IsAbs(cleanName) {
		return "", fmt.Errorf("archive entry %s uses an absolute path", entryName)
	}

	targetPath := filepath.Join(destDir, cleanName)
	relPath, err := filepath.Rel(destDir, targetPath)
	if err != nil {
		return "", fmt.Errorf("resolve archive path %s: %w", entryName, err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %s escapes the extraction directory", entryName)
	}

	return targetPath, nil
}

func resolveDirectoryRoot(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("read directory %s: %w", path, err)
	}

	var candidateDirs []string
	var visibleFiles int

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			candidateDirs = append(candidateDirs, filepath.Join(path, name))
			continue
		}
		if isSupportedArchive(name) {
			continue
		}
		visibleFiles++
	}

	if len(candidateDirs) != 1 || visibleFiles != 0 {
		return path, nil
	}
	if !looksLikeBundleRoot(candidateDirs[0]) {
		return path, nil
	}

	return candidateDirs[0], nil
}

func looksLikeBundleRoot(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if _, ok := bundleRootMarkers[strings.ToLower(entry.Name())]; ok {
			return true
		}
	}
	return false
}

func isSupportedArchive(path string) bool {
	lowerPath := strings.ToLower(path)
	return strings.HasSuffix(lowerPath, ".tar.gz") || strings.HasSuffix(lowerPath, ".tgz") || strings.HasSuffix(lowerPath, ".tar")
}
