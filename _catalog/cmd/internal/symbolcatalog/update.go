// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Update merges extraction into an existing regular file. The caller must ensure
// exclusive access to the destination until Update returns. The final content
// check detects unexpected changes but is not an atomic compare-and-swap.
func Update(file string, inv Inventory) (err error) {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("in-place catalog update requires an existing regular file")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	c, err := Decode(original)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	merged, err := Merge(c, inv)
	if err != nil {
		return err
	}
	data, err := Encode(merged)
	if err != nil {
		return err
	}
	if bytes.Equal(original, data) {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(file), ".symbolcatalog-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	current, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("catalog changed during extraction merge; refusing to overwrite concurrent edits")
	}
	return os.Rename(temp.Name(), file)
}
