package media

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type openedFileReader interface {
	File() *os.File
}

type processInput struct {
	name           string
	stdin          io.Reader
	extraFiles     []*os.File
	sensitivePaths []string
	cleanup        func()
}

func fileFromReader(reader io.Reader) *os.File {
	switch value := reader.(type) {
	case *os.File:
		return value
	case openedFileReader:
		return value.File()
	}
	return nil
}

func inputForProcess(reader io.Reader) processInput {
	file := fileFromReader(reader)
	if file != nil {
		return processInput{
			name:       "/dev/fd/3",
			extraFiles: []*os.File{file},
		}
	}
	return processInput{name: "pipe:0", stdin: reader}
}

func inputForSeekableProcess(reader io.Reader, workDir string) (processInput, error) {
	if runtime.GOOS != "windows" {
		return inputForProcess(reader), nil
	}

	file, err := os.CreateTemp(workDir, "source-*")
	if err != nil {
		return processInput{}, fmt.Errorf("create process input: %w", err)
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := io.Copy(file, reader); err != nil {
		file.Close()
		cleanup()
		return processInput{}, fmt.Errorf("copy process input: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return processInput{}, fmt.Errorf("close process input: %w", err)
	}

	return processInput{
		name:    filepath.Base(name),
		cleanup: cleanup,
	}, nil
}

func inputForFastSeekProcess(reader io.Reader, workDir string) (processInput, error) {
	if runtime.GOOS != "windows" {
		return inputForProcess(reader), nil
	}
	if file := fileFromReader(reader); file != nil && file.Name() != "" {
		name := file.Name()
		return processInput{
			name:           name,
			sensitivePaths: []string{name},
		}, nil
	}
	return inputForSeekableProcess(reader, workDir)
}
