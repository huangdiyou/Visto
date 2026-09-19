package mediaruntime

import (
	"archive/tar"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"

	"github.com/ulikunitz/xz"
)

// Identical rejection rules on every OS, including Windows aliases on Unix.
func memberName(name string) error {
	if name == "" || strings.ContainsAny(name, `\:`) || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return bad("unsafe archive path")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return bad("control character in archive path")
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.TrimRight(part, " .") != part || part == "." || part == ".." {
			return bad("unsafe archive component")
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return bad("reserved archive name")
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// The destination is a new private directory, never an installed release.
// os.Root supplies a second containment boundary against path traversal.
func extract(ctx context.Context, archive, format, destination string) error {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := map[string]bool{}
	var total int64
	count := 0
	write := func(name string, size int64, mode os.FileMode, reader io.Reader) error {
		name = strings.TrimSuffix(name, "/")
		if err := memberName(name); err != nil {
			return err
		}
		count++
		if count > 4096 {
			return bad("runtime archive has too many members")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return bad("duplicate archive member")
		}
		seen[key] = true
		if mode.IsDir() {
			return root.MkdirAll(name, 0755)
		}
		if !mode.IsRegular() || size < 0 || size > MaxExpandedBytes-total {
			return bad("runtime archive exceeds size or file type limit")
		}
		total += size
		if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
			return err
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		n, copyErr := io.CopyN(f, contextReader{ctx, reader}, size)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size {
			return bad("truncated runtime member")
		}
		if strings.HasPrefix(name, "bin/") {
			return root.Chmod(name, 0755)
		}
		return nil
	}
	if format == "zip" {
		r, err := zip.OpenReader(archive)
		if err != nil {
			return bad("invalid ZIP runtime")
		}
		defer r.Close()
		if len(r.File) > 4096 {
			return bad("runtime archive has too many members")
		}
		for _, f := range r.File {
			if f.UncompressedSize64 > uint64(MaxExpandedBytes) {
				return bad("runtime archive member exceeds limit")
			}
			body, err := f.Open()
			if err != nil {
				return err
			}
			err = write(f.Name, int64(f.UncompressedSize64), f.Mode(), body)
			// Read to EOF to validate ZIP checksum, including empty files.
			if err == nil {
				var b [1]byte
				n, e := body.Read(b[:])
				if n != 0 || e != io.EOF {
					err = bad("runtime ZIP checksum or member size mismatch")
				}
			}
			body.Close()
			if err != nil {
				return err
			}
		}
	} else if format == "tar.xz" {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		compressed, err := (xz.ReaderConfig{DictCap: 64 << 20, SingleStream: true}).NewReader(contextReader{ctx, f})
		if err != nil {
			return bad("invalid XZ runtime")
		}
		// Bound decoded stream, including padding/metadata not counted by tar file sizes.
		limited := &io.LimitedReader{R: contextReader{ctx, compressed}, N: MaxExpandedBytes + (16 << 20)}
		r := tar.NewReader(limited)
		for {
			h, e := r.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return bad("invalid runtime tar stream")
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return bad("runtime archive links or special members are forbidden")
			}
			if e = write(h.Name, h.Size, h.FileInfo().Mode(), r); e != nil {
				return e
			}
		}
		if _, err = io.Copy(io.Discard, limited); err != nil {
			return err
		}
		if limited.N == 0 {
			return bad("runtime archive expanded stream exceeds limit")
		}
	} else {
		return bad("unsupported runtime archive")
	}
	for _, name := range []string{"LICENSE.txt", "FFmpeg-BUILD.txt", "SOURCE.txt", "THIRD_PARTY_NOTICES.md", "THIRD_PARTY.spdx.json"} {
		f, e := root.Open(name)
		if e != nil {
			return bad(fmt.Sprintf("runtime is missing %s", name))
		}
		info, e := f.Stat()
		f.Close()
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return bad("invalid runtime evidence file")
		}
	}
	return nil
}
