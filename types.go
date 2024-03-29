package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type void struct{}

// Data is the generic content map passed on to the page template.
type Data map[string]interface{}
type page struct {
	Base string
	Data
	Title string
}

type branch struct {
	Commits []commit
	Name    string
	Project string
}

func (b branch) String() string {
	return b.Name
}

type diff struct {
	Body   string
	Commit commit
	Parent string
}

type overview struct {
	Body   string
	Hash   string
	Parent string
}

type hash struct {
	Hash  string
	Short string
}

func (h hash) String() string {
	return h.Hash
}

type object struct {
	Hash string
	Path string
}

func (o object) Dir() string {
	return filepath.Join(o.Hash[0:2], o.Hash[2:])
}

type show struct {
	Body  string
	Bin   bool
	Lines []int
	object
}

type commit struct {
	Branch  string
	Body    string
	Abbr    string
	History []overview
	Parents []string
	Hash    string
	Author  author
	Date    time.Time
	Project string
	Tree    []object
	Types   map[string]bool
	Subject string
}

type author struct {
	Email string
	Name  string
}

// https://stackoverflow.com/questions/28322997/how-to-get-a-list-of-values-into-a-flag-in-golang/
type manyflag []string

func (f *manyflag) Set(value string) error {
	// Make sure there are no duplicates.
	if !contains(*f, value) {
		*f = append(*f, value)
	}

	return nil
}

func (f *manyflag) String() string {
	return strings.Join(*f, ", ")
}

type options struct {
	Branches manyflag `json:"branches"`
	config   string
	Export   bool   `json:"export"`
	Force    bool   `json:"force"`
	Name     string `json:"name"`
	Quiet    bool   `json:"quiet"`
	Source   string `json:"source"`
	Template string `json:"template"`
}

// Helps store options as JSON.
func (o *options) save(p string) error {
	bs, err := json.MarshalIndent(o, "", "  ")

	if err != nil {
		return fmt.Errorf("failed to encode options: %v", err)
	}

	if err := os.WriteFile(filepath.Join(p, o.config), bs, 0644); err != nil {
		return fmt.Errorf("failed to write options: %v", err)
	}

	return nil
}
