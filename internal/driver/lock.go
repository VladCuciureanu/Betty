package driver

import (
	"os"
	"path/filepath"
)

// lock takes an exclusive advisory lock on the driver root, shared by every
// bt process, and returns the function that releases it. Installs, removals
// and storage read-modify-write cycles hold it so that concurrent commands
// don't lose each other's changes.
func (m *Manager) lock() (func(), error) {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(m.root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = unlockFile(file)
		file.Close()
	}, nil
}
