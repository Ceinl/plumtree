package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	repoFlag := flag.String("repo", "", "Plumtree repository root")
	outFlag := flag.String("out", "", "output bundle path")
	flag.Parse()
	if *repoFlag == "" || *outFlag == "" {
		fatalf("-repo and -out are required")
	}
	repo, err := filepath.Abs(*repoFlag)
	if err != nil {
		fatalf("resolve repository root: %v", err)
	}
	out, err := filepath.Abs(*outFlag)
	if err != nil {
		fatalf("resolve output path: %v", err)
	}
	if err := generate(repo, out); err != nil {
		fatalf("generate build assets: %v", err)
	}
}

func generate(repo, output string) error {
	tmp, err := os.MkdirTemp("", "plumtree-build-assets-generate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	moduleCache := filepath.Join(tmp, "modcache")
	for _, module := range []string{"sdk", "tui-runtime"} {
		cmd := exec.Command("go", "mod", "download", "all")
		cmd.Dir = filepath.Join(repo, module)
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOMODCACHE="+moduleCache)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download %s dependencies: %w\n%s", module, err, output)
		}
	}

	type source struct{ disk, archive string }
	var sources []source
	for _, module := range []string{"sdk", "tui-runtime"} {
		moduleRoot := filepath.Join(repo, module)
		err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(moduleRoot, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if rel != "." && (entry.Name() == ".git" || entry.Name() == "examples") {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			sources = append(sources, source{disk: path, archive: filepath.ToSlash(filepath.Join(module, rel))})
			return nil
		})
		if err != nil {
			return err
		}
	}

	proxyRoot := filepath.Join(moduleCache, "cache", "download")
	if err := filepath.WalkDir(proxyRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(moduleCache, path)
		if err != nil {
			return err
		}
		sources = append(sources, source{disk: path, archive: filepath.ToSlash(filepath.Join("modproxy", rel))})
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].archive < sources[j].archive })

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	tmpOutput := output + ".tmp"
	file, err := os.Create(tmpOutput)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(file)
	zw.Header.ModTime = time.Unix(0, 0)
	zw.Header.OS = 255
	tw := tar.NewWriter(zw)
	for _, source := range sources {
		info, err := os.Stat(source.disk)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name: source.archive, Mode: 0o444, Size: info.Size(), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0), AccessTime: time.Unix(0, 0), ChangeTime: time.Unix(0, 0),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		input, err := os.Open(source.disk)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpOutput, output); err != nil {
		return err
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
