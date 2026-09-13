package storage

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

func checkLocal(root string) error {
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return failure("The directory does not exist.", err)
	case errors.Is(err, fs.ErrPermission):
		return failure("The server has no permission to open the directory.", err)
	case err != nil:
		return failure("The server cannot open the directory.", err)
	case !info.IsDir():
		return failure("The path is not a directory.", nil)
	}

	file, err := os.CreateTemp(root, ".stocat-check-*")
	if err != nil {
		return failure("The server cannot write to the directory.", err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	_, err = file.Write(checkPayload)
	if err = errors.Join(err, file.Sync(), file.Close()); err != nil {
		return failure("The server cannot write to the directory.", err)
	}
	content, err := os.ReadFile(name)
	if err != nil {
		return failure("The server cannot read files in the directory.", err)
	}
	if !bytes.Equal(content, checkPayload) {
		return failure("The directory returned different content than the server wrote.", nil)
	}
	if err := os.Remove(name); err != nil {
		return failure("The server cannot delete files in the directory.", err)
	}
	return nil
}
