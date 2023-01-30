package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SEP is a browser generated UUID v4 used to separate out commit line items.
const SEP = "6f6c1745-e902-474a-9e99-08d0084fb011"

// Helps keep track of file extensions git thinks of as binary.
var types = make(map[string]bool)

type project struct {
	base     string
	Branches []branch
	Name     string
	repo     string
}

// Creates base directories for holding objects, branches, and commits.
func (pro *project) init(f bool) error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(pro.base, dir)

		// Clear existing dirs when -f true.
		if f && dir != "branch" {
			if err := os.RemoveAll(d); err != nil {
				return fmt.Errorf("unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("unable to create directory: %v", err)
		}
	}

	return nil
}

// Saves a local clone of `target` repo.
func (pro *project) save(target string) error {
	if _, err := os.Stat(pro.repo); err != nil {
		return err
	}

	return exec.Command("git", "clone", target, pro.repo).Run()
}

// Goes through list of branches and returns those that match whitelist.
func (pro *project) branchfilter(whitelist manyflag) ([]branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var b = make(map[string]branch)
	var m = make(map[string]bool)

	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		t := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "*"))
		_, f := filepath.Split(t)

		m[f] = true
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Filter to match options, but return all if no branch flags given.
	if len(whitelist) > 0 {
		for k := range m {
			m[k] = contains(whitelist, k)
		}
	} else {
		// In git given order at this point.
		for k := range m {
			whitelist = append(whitelist, k)
		}
	}

	for k, v := range m {
		if v {
			// TODO: Try a goroutine?
			commits, err := pro.commitparser(k)

			if err != nil {
				continue
			}

			b[k] = branch{commits, k, pro.Name}
		}
	}

	// Fill in resulting slice with desired branches in order.
	var results []branch

	for _, v := range whitelist {
		results = append(results, b[v])
	}

	return results, nil
}

func (pro *project) commitparser(b string) ([]commit, error) {
	fst := strings.Join([]string{"%H", "%P", "%s", "%aN", "%aE", "%aD", "%h"}, SEP)
	ref := fmt.Sprintf("origin/%s", b)

	cmd := exec.Command("git", "log", fmt.Sprintf("--format=%s", fst), ref)
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	results := []commit{}
	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		data := strings.Split(text, SEP)

		h := data[0]

		var history []overview
		var parents []string

		if data[1] != "" {
			parents = strings.Split(data[1], " ")
		}

		for _, p := range parents {
			diffstat, err := pro.diffstatparser(h, p)

			if err != nil {
				log.Printf("unable to diffstat against parent: %s", err)

				continue
			}

			history = append(history, overview{diffstat, h, p})
		}

		a := author{data[4], data[3]}

		date, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", data[5])

		if err != nil {
			log.Printf("unable to parse commit date: %s", err)

			continue
		}

		body, err := pro.bodyparser(h)

		if err != nil {
			log.Printf("unable to parse commit body: %s", err)

			continue
		}

		tree, err := pro.treeparser(h)

		if err != nil {
			log.Printf("unable to parse commit tree: %s", err)

			continue
		}

		c := commit{
			Abbr:    data[6],
			Author:  a,
			Body:    body,
			Branch:  b,
			Date:    date,
			Hash:    h,
			History: history,
			Parents: parents,
			Project: pro.Name,
			Subject: data[2],
			Tree:    tree,
		}

		results = append(results, c)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func (pro *project) treeparser(h string) ([]object, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "--format=%(objectname) %(path)", h)
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var results []object
	feed := strings.Split(strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), "\n")

	for _, line := range feed {
		w := strings.Split(line, " ")

		results = append(results, object{
			Hash: w[0],
			Path: w[1],
		})
	}

	return results, nil
}

func (pro *project) diffstatparser(h, p string) (string, error) {
	cmd := exec.Command("git", "diff", "--stat", fmt.Sprintf("%s..%s", p, h))
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	var results []string
	feed := strings.Split(strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), "\n")

	for _, line := range feed {
		// NOTE: This is hackish I know, attach to project?
		i := strings.Index(line, "|")

		if i != -1 {
			ext := filepath.Ext(strings.TrimSpace(line[:i]))
			types[ext] = strings.Contains(line, "Bin")
		}

		results = append(results, strings.TrimSpace(line))
	}

	return strings.Join(results, "\n"), nil
}

func (pro *project) bodyparser(h string) (string, error) {
	// Because the commit message body is multiline and is tripping the scanner.
	cmd := exec.Command("git", "show", "--no-patch", "--format=%B", h)
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), nil
}
