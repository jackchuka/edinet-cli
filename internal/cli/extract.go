package cli

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackchuka/edinet-cli/internal/edinet"
)

// extractZip unpacks a downloaded archive into dir/<docID>/.
func extractZip(file *edinet.File, dir string, force bool) error {
	if file.Type == edinet.FilePDF {
		return fmt.Errorf("--extract does not apply to --file pdf")
	}

	zr, err := zip.NewReader(bytes.NewReader(file.Data), int64(len(file.Data)))
	if err != nil {
		return fmt.Errorf("reading the downloaded archive: %w", err)
	}

	root := filepath.Join(dir, file.DocID)
	if !force {
		if _, err := os.Stat(root); err == nil {
			return fmt.Errorf("%s already exists (pass --force to overwrite)", root)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	var count int
	for _, f := range zr.File {
		dest, err := safeJoin(root, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, dest); err != nil {
			return err
		}
		count++
	}

	fmt.Fprintf(os.Stderr, "Extracted %d files to %s\n", count, root)
	return nil
}

func writeZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return out.Close()
}

// safeJoin refuses archive entries that would escape the destination directory.
//
// filepath.Join already contains an absolute entry name by making it relative
// to root, but such a name means the archive is malformed, so it is rejected
// rather than quietly rewritten.
func safeJoin(root, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("refusing to extract %q: archive entries must use relative paths", name)
	}
	dest := filepath.Join(root, filepath.FromSlash(name))
	prefix := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(dest, prefix) {
		return "", fmt.Errorf("refusing to extract %q: it points outside %s", name, root)
	}
	return dest, nil
}
