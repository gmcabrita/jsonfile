// Copyright (c) 2024 David Crawshaw
// Copyright (c) 2026 Gonçalo Mendes Cabrita
// SPDX-License-Identifier: BSD-3-Clause

package jsonfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func mustWrite[Data any](t *testing.T, file *JSONFile[Data], fn func(*Data)) {
	t.Helper()
	if err := file.Write(func(data *Data) error {
		fn(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNewWriteAndLoad(t *testing.T) {
	t.Parallel()

	type Data struct {
		Name    string         `json:"name"`
		Friends []string       `json:"friends"`
		Ages    map[string]int `json:"ages"`
	}
	want := Data{
		Name:    "Alice",
		Friends: []string{"Bob", "Carol", "Dave"},
		Ages:    map[string]int{"Bob": 25, "Carol": 30, "Dave": 35},
	}
	path := filepath.Join(t.TempDir(), "data.json")

	file, err := New[Data](path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, file, func(data *Data) {
		*data = want
	})
	mustWrite(t, file, func(*Data) {})

	file.Read(func(data *Data) {
		if !reflect.DeepEqual(*data, want) {
			t.Errorf("Read() = %#v, want %#v", *data, want)
		}
	})

	loaded, err := Load[Data](path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Read(func(data *Data) {
		if !reflect.DeepEqual(*data, want) {
			t.Errorf("loaded Read() = %#v, want %#v", *data, want)
		}
	})
}

func TestNewCreatesEmptyObject(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.json")
	if _, err := New[struct{}](path); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "{}" {
		t.Fatalf("contents = %q, want %q", contents, "{}")
	}
}

func TestWriteRollsBackCallbackError(t *testing.T) {
	t.Parallel()

	type Data struct {
		Value int `json:"value"`
	}
	path := filepath.Join(t.TempDir(), "rollback.json")
	file, err := New[Data](path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, file, func(data *Data) { data.Value = 1 })

	rollbackErr := errors.New("rollback")
	err = file.Write(func(data *Data) error {
		data.Value = 2
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("Write() error = %v, want %v", err, rollbackErr)
	}

	file.Read(func(data *Data) {
		if data.Value != 1 {
			t.Errorf("Value = %d, want 1", data.Value)
		}
	})
	loaded, err := Load[Data](path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Read(func(data *Data) {
		if data.Value != 1 {
			t.Errorf("persisted Value = %d, want 1", data.Value)
		}
	})
}

func TestWriteDoesNotRetainAliases(t *testing.T) {
	t.Parallel()

	type Data struct {
		Values []int `json:"values"`
	}
	file, err := New[Data](filepath.Join(t.TempDir(), "aliases.json"))
	if err != nil {
		t.Fatal(err)
	}

	values := []int{1, 2, 3}
	mustWrite(t, file, func(data *Data) { data.Values = values })
	values[0] = 10

	file.Read(func(data *Data) {
		if !slices.Equal(data.Values, []int{1, 2, 3}) {
			t.Errorf("Values = %v, want [1 2 3]", data.Values)
		}
	})
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	type Data struct {
		Value int `json:"value"`
	}
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := Load[Data](path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load() error = %v, want fs.ErrNotExist", err)
	}

	if err := os.WriteFile(path, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load[Data](path); err == nil {
		t.Fatal("Load() succeeded with invalid JSON")
	}
}

func TestNewReportsRenameError(t *testing.T) {
	t.Parallel()

	type Data struct {
		Value int `json:"value"`
	}
	directory := t.TempDir()
	if _, err := New[Data](directory); err == nil {
		t.Fatal("New() succeeded with a directory path")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(directory), filepath.Base(directory)+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary files were not removed: %v", matches)
	}
}

func TestAtomicWriteSyncsBeforeCloseAndRename(t *testing.T) {
	t.Parallel()

	fsys := &recordingFileSystem{}
	if err := atomicWriteFile(fsys, "/data/data.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "write", "sync", "close", "rename"}
	if !slices.Equal(fsys.events, want) {
		t.Fatalf("operations = %v, want %v", fsys.events, want)
	}
	if fsys.renamedFrom != recordingTempPath || fsys.renamedTo != "/data/data.json" {
		t.Errorf("Rename(%q, %q), want Rename(%q, %q)", fsys.renamedFrom, fsys.renamedTo, recordingTempPath, "/data/data.json")
	}
}

func TestAtomicWriteStopsWhenSyncFails(t *testing.T) {
	t.Parallel()

	syncErr := errors.New("sync failed")
	fsys := &recordingFileSystem{syncErr: syncErr}
	err := atomicWriteFile(fsys, "/data/data.json", []byte("{}"))
	if !errors.Is(err, syncErr) {
		t.Fatalf("atomicWriteFile() error = %v, want %v", err, syncErr)
	}
	want := []string{"create", "write", "sync", "close", "remove"}
	if !slices.Equal(fsys.events, want) {
		t.Fatalf("operations = %v, want %v", fsys.events, want)
	}
	if fsys.renamedFrom != "" {
		t.Errorf("renamed temporary file after Sync failed")
	}
}

const recordingTempPath = "/data/data.json.tmp-test"

type recordingFileSystem struct {
	events      []string
	syncErr     error
	renamedFrom string
	renamedTo   string
}

func (fsys *recordingFileSystem) CreateTemp(_, _ string) (syncedFile, error) {
	fsys.events = append(fsys.events, "create")
	return &recordingFile{fsys: fsys}, nil
}

func (fsys *recordingFileSystem) Rename(oldPath, newPath string) error {
	fsys.events = append(fsys.events, "rename")
	fsys.renamedFrom = oldPath
	fsys.renamedTo = newPath
	return nil
}

func (fsys *recordingFileSystem) Remove(string) error {
	fsys.events = append(fsys.events, "remove")
	return nil
}

type recordingFile struct {
	fsys *recordingFileSystem
}

func (file *recordingFile) Write(contents []byte) (int, error) {
	file.fsys.events = append(file.fsys.events, "write")
	return len(contents), nil
}

func (*recordingFile) Name() string {
	return recordingTempPath
}

func (file *recordingFile) Sync() error {
	file.fsys.events = append(file.fsys.events, "sync")
	return file.fsys.syncErr
}

func (file *recordingFile) Close() error {
	file.fsys.events = append(file.fsys.events, "close")
	return nil
}
