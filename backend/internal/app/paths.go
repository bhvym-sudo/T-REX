package app

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Root       string
	Config     string
	Runtime    string
	QueryIDs   string
	Logs       string
	Exports    string
	SessionDir string
}

func ResolvePaths() Paths {
	root := os.Getenv("TREX_DATA_DIR")
	if root == "" {
		if value, err := os.Getwd(); err == nil {
			root = value
		}
	}
	if root == "" {
		root = "."
	}
	root, _ = filepath.Abs(root)
	return Paths{
		Root:       root,
		Config:     filepath.Join(root, "config"),
		Runtime:    filepath.Join(root, "config", "runtime.json"),
		QueryIDs:   filepath.Join(root, "config", "query_ids.json"),
		Logs:       filepath.Join(root, "logs"),
		Exports:    filepath.Join(root, "exports"),
		SessionDir: filepath.Join(root, "sessions", "x_edge_profile"),
	}
}

func (p Paths) Ensure() error {
	for _, path := range []string{p.Config, p.Logs, p.Exports, p.SessionDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}
