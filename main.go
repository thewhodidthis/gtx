package main

import (
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"text/template"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const configFile = ".ht_git2html"

//go:embed config.tmpl
var tmpl string

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

	// if len(args) != 2 {
	// 	log.Fatalf("jimmy: please specify a single target path")
	// }

	targetDir := args[len(args)-1]

	if ok := filepath.IsAbs(targetDir); !ok {
		cwd, err := os.Getwd()

		if err != nil {
			log.Fatalf("jimmy: %v", err)
		}

		targetDir = filepath.Join(cwd, targetDir)
	}

	// TODO: Look up more mode for 755 or 644.
	if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
		log.Fatalf("jimmy: unable to create target directory: %v", err)
	}

	configTmpl := template.Must(template.New("default").Parse(tmpl))

	// TODO: Check file permissions are set to 0666.
	// TODO: Read file if it exists.
	outFile, err := os.Create(filepath.Join(targetDir, configFile))

	if err != nil {
		log.Fatalf("jimmy: unable to create config file: %v", err)
	}

	h := sha1.New()

	if _, err := io.Copy(h, outFile); err != nil {
		log.Fatal(err)
	}

	configTmpl.Execute(outFile, struct {
		Project          string
		Repository       string
		PublicRepository string
		Target           string
		Branches         string
		// SHA1SUM
		Template string
	}{
		Project:          p,
		Repository:       r,
		PublicRepository: l,
		Target:           targetDir,
		Branches:         b,
		Template:         hex.EncodeToString(h.Sum(nil)),
	})

	// Repository
	dirs := []string{"branches", "commits", "objects"}

	for _, dir := range dirs {
		d := filepath.Join(targetDir, dir)

		// Clear existing dirs if force true.
		if f && dir != "branches" {
			if err := os.RemoveAll(d); err != nil {
				log.Printf("jimmy: unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, os.ModePerm); err != nil {
			log.Printf("jimmy: unable to create directory: %v", err)
		}
	}

	var pathError *fs.PathError
	repo := filepath.Join(targetDir, "repository")

	_, err = os.Stat(repo)

	if errors.As(err, &pathError) {
		ro, err := git.PlainClone(repo, false, &git.CloneOptions{
			URL:      r,
			Progress: os.Stdout,
		})

		co, err := ro.CommitObjects()

		if err != nil {
			log.Printf("%v", err)
		}

		co.ForEach(func(c *object.Commit) error {
			log.Print(c)
			return nil
		})

		branches, err := ro.Branches()

		if err != nil {
			log.Printf("%v", err)
		}

		branch, err := branches.Next()

		if err != nil {
			log.Printf("jimmy: failed to clone repo: %v", err)
		}

		ref := plumbing.NewHashReference(branch.Name(), branch.Hash())

		if err != nil {
			log.Printf("jimmy: failed to clone repo: %v", err)
		}

		w, err := ro.Worktree()

		if err != nil {
			log.Printf("jimmy: failed to clone repo: %v", err)
		}

		err = w.Checkout(&git.CheckoutOptions{
			Hash: ref.Hash(),
		})

		if err != nil {
			log.Printf("jimmy: failed to clone repo: %v", err)
		}

		err = ro.Storer.RemoveReference(ref.Name())

		if err != nil {
			log.Printf("jimmy: failed to clone repo: %v", err)
		}
	}
}
