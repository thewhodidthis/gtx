package main

import (
	_ "embed"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"text/template"
)

const configFile = ".ht_git2html"

//go:embed config.tmpl
var tmpl string

// !goimports -w %
func main() {
	var p string
	var r string
	var l string
	var b string
	var q bool
	var f bool

	flag.StringVar(&p, "p", "My project", "Choose a project name")
	flag.StringVar(&r, "r", "/path/to/repo", "Repository to clone from")
	flag.StringVar(&l, "l", "http://host.org/project.git", "Public link to repo")
	flag.StringVar(&b, "b", "all", "List of branches")
	flag.BoolVar(&q, "q", false, "Be quiet")
	flag.BoolVar(&f, "f", false, "Force rebuilding of all pages")
	flag.Parse()

	log.Printf("%v %v %v %v %v %v", p, r, l, b, q, f)

	args := os.Args

	if len(args) != 2 {
		log.Fatalf("jimmy: please specify a single target path")
	}

	t := args[1]

	if ok := filepath.IsAbs(t); !ok {
		cwd, err := os.Getwd()

		if err != nil {
			log.Fatalf("jimmy: %v", err)
		}

		t = filepath.Join(cwd, t)
	}

	// TODO: Look up more mode for 755 or 644.
	if err := os.MkdirAll(t, os.ModePerm); err != nil {
		log.Fatalf("jimmy: unable to create target directory: %v", err)
	}

	configTmpl := template.Must(template.New("default").Parse(tmpl))

	// TODO: Check file permissions are set to 0666.
	// TODO: Read file if it exists.
	outFile, err := os.Create(filepath.Join(t, configFile))

	if err != nil {
		log.Fatalf("jimmy: unable to create config file: %v", err)
	}

	h := sha1.New()

	if _, err := io.Copy(h, outFile); err != nil {
		log.Fatal(err)
	}

	configTmpl.Execute(outFile, struct {
		Project string
		Repository string
		PublicRepository string
		Target string
		Branches string
		// SHA1SUM
		Template string
	}{
		Project: p,
		Repository: r,
		PublicRepository: l,
		Target: t,
		Branches: b,
		Template: hex.EncodeToString(h.Sum(nil)),
	})
}
