package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type options struct {
	Branches manyflag `json:"branches"`
	config   string
	Force    bool   `json:"force"`
	Name     string `json:"name"`
	Quiet    bool   `json:"quiet"`
	Source   string `json:"source"`
	Template string `json:"template"`
	URL      string `json:"url"`
}

// Helps store options into a JSON config file.
func (o *options) save(p string) error {
	bs, err := json.MarshalIndent(o, "", "  ")

	if err != nil {
		return fmt.Errorf("unable to encode config file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(p, o.config), bs, 0644); err != nil {
		return fmt.Errorf("unable to save config file: %v", err)
	}

	return nil
}
