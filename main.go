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
	var project string
	var repo string
	var link string
	var branches string
	var quiet bool
	var force bool

	flag.StringVar(&project, "p", "My project", "Choose a project name")
	flag.StringVar(&repo, "r", "/path/to/repo", "Repository to clone from")
	flag.StringVar(&link, "l", "http://host.org/project.git", "Public link to repo")
	flag.StringVar(&branches, "b", "all", "List of branches")
	flag.BoolVar(&quiet, "q", false, "Be quiet")
	flag.BoolVar(&force, "f", false, "Force rebuilding of all pages")
	flag.Parse()

	log.Printf("%v %v %v %v %v %v", project, repo, link, branches, quiet, force)

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
		Project:          project,
		Repository:       repo,
		PublicRepository: link,
		Target:           targetDir,
		Branches:         branches,
		Template:         hex.EncodeToString(h.Sum(nil)),
	})

	// Repository
	dirs := []string{"branches", "commits", "objects"}

	for _, dir := range dirs {
		d := filepath.Join(targetDir, dir)

		// Clear existing dirs if force true.
		if force && dir != "branches" {
			if err := os.RemoveAll(d); err != nil {
				log.Printf("jimmy: unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, os.ModePerm); err != nil {
			log.Printf("jimmy: unable to create directory: %v", err)
		}
	}

	var pathError *fs.PathError
	repoPath := filepath.Join(targetDir, "repository")

	_, err = os.Stat(repoPath)

	if errors.As(err, &pathError) {
		localRepo, err := git.PlainClone(repoPath, false, &git.CloneOptions{
			URL:      repo,
			Progress: os.Stdout,
		})

		commitObjects, err := localRepo.CommitObjects()

		if err != nil {
			log.Printf("%v", err)
		}

		commitObjects.ForEach(func(c *object.Commit) error {
			log.Print(c)
			return nil
		})

		localBranches, err := localRepo.Branches()

		if err != nil {
			log.Printf("%v", err)
		}

		branch, err := localBranches.Next()

		if err != nil {
			log.Printf("jimmy: failed to list branches: %v", err)
		}

		ref := plumbing.NewHashReference(branch.Name(), branch.Hash())

		if err != nil {
			log.Printf("jimmy: failed to create ref: %v", err)
		}

		workTree, err := localRepo.Worktree()

		if err != nil {
			log.Printf("jimmy: failed to open worktree: %v", err)
		}

		err = workTree.Checkout(&git.CheckoutOptions{
			Hash: ref.Hash(),
		})

		if err != nil {
			log.Printf("jimmy: failed to checkout detached HEAD: %v", err)
		}

		err = localRepo.Storer.RemoveReference(ref.Name())

		if err != nil {
			log.Printf("jimmy: failed to delete branch: %v", err)
		}
	}
}
