# jsonfile

`jsonfile` persists a small Go value to one JSON file. It is intended for
prototypes with frequent reads, infrequent writes, and no need for indexes.

```go
package main

import (
	"errors"
	"io/fs"
	"log"

	"github.com/gmcabrita/jsonfile"
)

type Data struct {
	Visits int `json:"visits"`
}

func main() {
	db, err := jsonfile.Load[Data]("data.json")
	if errors.Is(err, fs.ErrNotExist) {
		db, err = jsonfile.New[Data]("data.json")
	}
	if err != nil {
		log.Fatal(err)
	}

	if err := db.Write(func(data *Data) error {
		data.Visits++
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	db.Read(func(data *Data) {
		log.Printf("visits: %d", data.Visits)
	})
}
```

## Guarantees

- `Write` changes an isolated copy and rolls back if its callback fails.
- Successful writes do not retain aliases supplied by the callback.
- Writes use a temporary file in the same directory, then `Sync`, `Close`, and
  rename in that order. Rename atomicity and durability depend on the operating
  system and file system; this package does not sync the parent directory.
- Read and write callbacks are synchronized within one process.

Treat values passed to `Read` as read-only and do not keep them after the
callback. This package does not coordinate multiple processes. Move to a real
database when the data needs indexes or frequent writes.

This is a Go 1.26.5 reimplementation of
[`crawshaw.dev/jsonfile`](https://github.com/crawshaw/jsonfile).
