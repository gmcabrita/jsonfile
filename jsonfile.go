// Copyright (c) 2024 David Crawshaw
// Copyright (c) 2026 Gonçalo Mendes Cabrita
// SPDX-License-Identifier: BSD-3-Clause

// Package jsonfile persists a Go value to a JSON file.
package jsonfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// JSONFile holds a Go value of type Data and persists it to a JSON file.
//
// Use [New] or [Load] to create a JSONFile. Read and Write callbacks run while
// the relevant lock is held. Data passed to Read must not be modified or kept
// after the callback returns.
type JSONFile[Data any] struct {
	path string

	mu    sync.RWMutex
	bytes []byte
	data  *Data
}

// New creates a JSONFile at path by decoding an empty JSON object into Data.
// It replaces an existing file where the operating system permits replacement.
func New[Data any](path string) (*JSONFile[Data], error) {
	file := &JSONFile[Data]{
		path:  path,
		bytes: []byte("{}"),
		data:  new(Data),
	}
	if err := file.write(func(*Data) error { return nil }, true); err != nil {
		return nil, fmt.Errorf("jsonfile.New: %w", err)
	}
	return file, nil
}

// Load loads an existing JSONFile from path.
//
// If the file does not exist, Load returns an error that can be checked with
// errors.Is(err, fs.ErrNotExist).
//
// Load and New are separate to prevent a service from silently creating a new
// file when its data file is missing. To load or create a file explicitly:
//
//	db, err := jsonfile.Load[Data](path)
//	if errors.Is(err, fs.ErrNotExist) {
//		db, err = jsonfile.New[Data](path)
//	}
func Load[Data any](path string) (*JSONFile[Data], error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("jsonfile.Load: %w", err)
	}

	data := new(Data)
	if err := json.Unmarshal(contents, data); err != nil {
		return nil, fmt.Errorf("jsonfile.Load: %w", err)
	}
	return &JSONFile[Data]{path: path, bytes: contents, data: data}, nil
}

// Read calls fn with the current data.
// The data must be treated as read-only and must not be kept after fn returns.
func (file *JSONFile[Data]) Read(fn func(data *Data)) {
	file.mu.RLock()
	defer file.mu.RUnlock()
	fn(file.data)
}

// Write calls fn with an isolated copy of the data, then atomically replaces
// the JSON file with the result. If fn or persistence fails, the in-memory data
// remains unchanged.
func (file *JSONFile[Data]) Write(fn func(*Data) error) error {
	return file.write(fn, false)
}

func (file *JSONFile[Data]) write(fn func(*Data) error, force bool) error {
	file.mu.Lock()
	defer file.mu.Unlock()

	data := new(Data)
	if err := json.Unmarshal(file.bytes, data); err != nil {
		return fmt.Errorf("jsonfile.JSONFile.Write: decode current data: %w", err)
	}
	if err := fn(data); err != nil {
		return err
	}

	contents, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("jsonfile.JSONFile.Write: encode data: %w", err)
	}
	if !force && bytes.Equal(contents, file.bytes) {
		return nil
	}

	// Decode before persistence so custom JSON methods cannot leave the file
	// and memory in different states after a successful rename.
	persisted := new(Data)
	if err := json.Unmarshal(contents, persisted); err != nil {
		return fmt.Errorf("jsonfile.JSONFile.Write: decode encoded data: %w", err)
	}
	if err := atomicWriteFile(osFileSystem{}, file.path, contents); err != nil {
		return fmt.Errorf("jsonfile.JSONFile.Write: %w", err)
	}

	file.data = persisted
	file.bytes = contents
	return nil
}

// syncedFile and fileSystem are the minimum I/O boundary needed to verify that
// a temporary file is synced and closed before it is renamed.
type syncedFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type fileSystem interface {
	CreateTemp(dir, pattern string) (syncedFile, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
}

type osFileSystem struct{}

func (osFileSystem) CreateTemp(dir, pattern string) (syncedFile, error) {
	return os.CreateTemp(dir, pattern)
}

func (osFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osFileSystem) Remove(path string) error {
	return os.Remove(path)
}

func atomicWriteFile(fsys fileSystem, path string, contents []byte) error {
	temp, err := fsys.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	tempPath := temp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		if !renamed {
			_ = fsys.Remove(tempPath)
		}
	}()

	written, err := temp.Write(contents)
	if err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if written != len(contents) {
		return fmt.Errorf("write temporary file: %w", io.ErrShortWrite)
	}
	// Sync before Close and Rename. Otherwise, a power loss can leave the
	// renamed file pointing to data that never reached stable storage.
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	closeErr := temp.Close()
	closed = true
	if closeErr != nil {
		return fmt.Errorf("close temporary file: %w", closeErr)
	}

	if err := fsys.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	renamed = true
	return nil
}
