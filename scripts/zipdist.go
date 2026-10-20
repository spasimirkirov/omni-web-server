//go:build ignore

// zipdist writes the portable zip: go run scripts/zipdist.go <out.zip> <files...>
// Paths are stored as given (relative to the working directory).
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/zipdist.go <out.zip> <files...>")
		os.Exit(2)
	}
	if err := write(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func write(out string, files []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for _, name := range files {
		src, err := os.Open(name)
		if err != nil {
			return err
		}
		st, err := src.Stat()
		if err != nil {
			src.Close()
			return err
		}
		h, err := zip.FileInfoHeader(st)
		if err != nil {
			src.Close()
			return err
		}
		h.Name, h.Method = filepath.ToSlash(name), zip.Deflate
		w, err := z.CreateHeader(h)
		if err == nil {
			_, err = io.Copy(w, src)
		}
		src.Close()
		if err != nil {
			return err
		}
	}
	return z.Close()
}
