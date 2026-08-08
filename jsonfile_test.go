// Copyright (c) 2024 David Crawshaw
// Copyright (c) 2026 Gonçalo Mendes Cabrita
// SPDX-License-Identifier: BSD-3-Clause

package jsonfile

import (
	"bytes"
	"errors"
	"io"
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

	fsys := &faultFileSystem{destination: []byte("old")}
	if err := atomicWriteFile(fsys, "/data/data.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "write", "sync", "close", "rename"}
	if !slices.Equal(fsys.events, want) {
		t.Fatalf("operations = %v, want %v", fsys.events, want)
	}
	if fsys.renamedFrom != faultTempPath || fsys.renamedTo != "/data/data.json" {
		t.Errorf("Rename(%q, %q), want Rename(%q, %q)", fsys.renamedFrom, fsys.renamedTo, faultTempPath, "/data/data.json")
	}
	if !bytes.Equal(fsys.destination, []byte("{}")) {
		t.Errorf("destination = %q, want %q", fsys.destination, "{}")
	}
	if fsys.removed {
		t.Error("removed temporary file after successful rename")
	}
}

func TestWriteStorageFailures(t *testing.T) {
	t.Parallel()

	diskFullErr := errors.New("disk full")
	storageErr := errors.New("storage I/O failure")
	tests := []struct {
		name        string
		fault       storageFault
		faultErr    error
		wantErr     error
		wantEvents  []string
		wantRemoved bool
		wantRename  bool
	}{
		{
			name:       "create temporary file",
			fault:      faultCreate,
			faultErr:   storageErr,
			wantErr:    storageErr,
			wantEvents: []string{"create"},
		},
		{
			name:        "disk full during write",
			fault:       faultWrite,
			faultErr:    diskFullErr,
			wantErr:     diskFullErr,
			wantEvents:  []string{"create", "write", "close", "remove"},
			wantRemoved: true,
		},
		{
			name:        "partial write",
			fault:       faultShortWrite,
			wantErr:     io.ErrShortWrite,
			wantEvents:  []string{"create", "write", "close", "remove"},
			wantRemoved: true,
		},
		{
			name:        "disk full during sync",
			fault:       faultSync,
			faultErr:    diskFullErr,
			wantErr:     diskFullErr,
			wantEvents:  []string{"create", "write", "sync", "close", "remove"},
			wantRemoved: true,
		},
		{
			name:        "failure during close",
			fault:       faultClose,
			faultErr:    storageErr,
			wantErr:     storageErr,
			wantEvents:  []string{"create", "write", "sync", "close", "remove"},
			wantRemoved: true,
		},
		{
			name:        "failure during rename",
			fault:       faultRename,
			faultErr:    storageErr,
			wantErr:     storageErr,
			wantEvents:  []string{"create", "write", "sync", "close", "rename", "remove"},
			wantRemoved: true,
			wantRename:  true,
		},
	}

	type Data struct {
		Value int `json:"value"`
	}
	original := []byte(`{"value":1}`)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fsys := &faultFileSystem{
				fault:       test.fault,
				faultErr:    test.faultErr,
				destination: bytes.Clone(original),
			}
			file := &JSONFile[Data]{
				path:  "/data/data.json",
				bytes: bytes.Clone(original),
				data:  &Data{Value: 1},
			}

			err := file.write(func(data *Data) error {
				data.Value = 2
				return nil
			}, false, fsys)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Write() error = %v, want %v", err, test.wantErr)
			}
			if !slices.Equal(fsys.events, test.wantEvents) {
				t.Errorf("operations = %v, want %v", fsys.events, test.wantEvents)
			}
			if fsys.removed != test.wantRemoved {
				t.Errorf("temporary file removed = %t, want %t", fsys.removed, test.wantRemoved)
			}
			if test.wantRemoved && fsys.removedPath != faultTempPath {
				t.Errorf("Remove(%q), want Remove(%q)", fsys.removedPath, faultTempPath)
			}
			if (fsys.renamedFrom != "") != test.wantRename {
				t.Errorf("rename attempted = %t, want %t", fsys.renamedFrom != "", test.wantRename)
			}
			if test.wantRename && (fsys.renamedFrom != faultTempPath || fsys.renamedTo != file.path) {
				t.Errorf("Rename(%q, %q), want Rename(%q, %q)", fsys.renamedFrom, fsys.renamedTo, faultTempPath, file.path)
			}
			if !bytes.Equal(fsys.destination, original) {
				t.Errorf("persisted data = %q, want unchanged %q", fsys.destination, original)
			}
			if !bytes.Equal(file.bytes, original) {
				t.Errorf("cached JSON = %q, want unchanged %q", file.bytes, original)
			}
			file.Read(func(data *Data) {
				if data.Value != 1 {
					t.Errorf("in-memory Value = %d, want unchanged 1", data.Value)
				}
			})

			fsys.fault = faultNone
			if err := file.write(func(data *Data) error {
				data.Value = 3
				return nil
			}, false, fsys); err != nil {
				t.Fatalf("Write() after failure: %v", err)
			}
			wantRecovered := []byte(`{"value":3}`)
			if !bytes.Equal(fsys.destination, wantRecovered) {
				t.Errorf("persisted data after recovery = %q, want %q", fsys.destination, wantRecovered)
			}
			file.Read(func(data *Data) {
				if data.Value != 3 {
					t.Errorf("in-memory Value after recovery = %d, want 3", data.Value)
				}
			})
		})
	}
}

const faultTempPath = "/data/data.json.tmp-test"

type storageFault uint8

const (
	faultNone storageFault = iota
	faultCreate
	faultWrite
	faultShortWrite
	faultSync
	faultClose
	faultRename
)

type faultFileSystem struct {
	fault       storageFault
	faultErr    error
	events      []string
	temporary   []byte
	destination []byte
	removed     bool
	removedPath string
	renamedFrom string
	renamedTo   string
}

func (fsys *faultFileSystem) CreateTemp(_, _ string) (syncedFile, error) {
	fsys.events = append(fsys.events, "create")
	if fsys.fault == faultCreate {
		return nil, fsys.faultErr
	}
	return &faultFile{fsys: fsys}, nil
}

func (fsys *faultFileSystem) Rename(oldPath, newPath string) error {
	fsys.events = append(fsys.events, "rename")
	fsys.renamedFrom = oldPath
	fsys.renamedTo = newPath
	if fsys.fault == faultRename {
		return fsys.faultErr
	}
	fsys.destination = bytes.Clone(fsys.temporary)
	fsys.temporary = nil
	return nil
}

func (fsys *faultFileSystem) Remove(path string) error {
	fsys.events = append(fsys.events, "remove")
	fsys.temporary = nil
	fsys.removed = true
	fsys.removedPath = path
	return nil
}

type faultFile struct {
	fsys *faultFileSystem
}

func (file *faultFile) Write(contents []byte) (int, error) {
	file.fsys.events = append(file.fsys.events, "write")
	switch file.fsys.fault {
	case faultWrite:
		return 0, file.fsys.faultErr
	case faultShortWrite:
		written := max(0, len(contents)-1)
		file.fsys.temporary = bytes.Clone(contents[:written])
		return written, nil
	default:
		file.fsys.temporary = bytes.Clone(contents)
		return len(contents), nil
	}
}

func (*faultFile) Name() string {
	return faultTempPath
}

func (file *faultFile) Sync() error {
	file.fsys.events = append(file.fsys.events, "sync")
	if file.fsys.fault == faultSync {
		return file.fsys.faultErr
	}
	return nil
}

func (file *faultFile) Close() error {
	file.fsys.events = append(file.fsys.events, "close")
	if file.fsys.fault == faultClose {
		return file.fsys.faultErr
	}
	return nil
}
