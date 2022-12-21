package upload

import (
	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func Upload(hash, target, folder string) error {
	cacheImageRef := strings.ReplaceAll(target, "{{hash}}", hash)
	fmt.Fprintf(os.Stderr, "Upload %s content to oci image %s\n", folder, cacheImageRef)

	// Try to fetch it (if it exists)
	base := empty.Image
	base = mutate.MediaType(base, types.OCIManifestSchema1)
	base = mutate.ConfigMediaType(base, types.OCIConfigJSON)

	file, err := ioutil.TempFile("", "cache.tar")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if err := tarit(folder, file.Name()); err != nil {
		return err
	}

	image, err := crane.Append(base, file.Name())
	if err != nil {
		// If not, warn and do not fail
		fmt.Fprintf(os.Stderr, "Warning: %s", err)
		return nil
	}

	if err := crane.Push(image, cacheImageRef); err != nil {
		return err
	}

	return nil
}

func tarit(source, target string) error {
	tarfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer tarfile.Close()

	tarball := tar.NewWriter(tarfile)
	defer tarball.Close()

	info, err := os.Stat(source)
	if err != nil {
		return nil
	}

	var baseDir string
	if info.IsDir() {
		baseDir = filepath.Base(source)
	}

	return filepath.Walk(source,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, info.Name())
			if err != nil {
				return err
			}

			if baseDir != "" {
				header.Name = filepath.Join(baseDir, strings.TrimPrefix(path, source))
			}

			if err := tarball.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(tarball, file)
			return err
		})
}
