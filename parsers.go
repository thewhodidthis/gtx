package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Goes through list of branches and returns those that match whitelist.
func branchFilter(repo string, options *options) ([]branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = repo

	whitelist := options.Branches

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
			commits, err := commitParser(k, repo, options.Name)

			if err != nil {
				continue
			}

			b[k] = branch{commits, k, options.Name}
		}
	}

	// Fill in resulting slice with desired branches in order.
	var results []branch

	for _, v := range whitelist {
		results = append(results, b[v])
	}

	return results, nil
}

func commitParser(b string, repo string, name string) ([]commit, error) {
	fst := strings.Join([]string{"%H", "%P", "%s", "%aN", "%aE", "%aD", "%h"}, SEP)
	ref := fmt.Sprintf("origin/%s", b)

	cmd := exec.Command("git", "log", fmt.Sprintf("--format=%s", fst), ref)
	cmd.Dir = repo

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

		for _, parent := range parents {
			diffstat, err := diffStatParser(h, parent, repo)

			if err != nil {
				log.Printf("unable to diffstat against parent: %s", err)

				continue
			}

			history = append(history, overview{diffstat, h, parent})
		}

		a := author{data[4], data[3]}

		date, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", data[5])

		if err != nil {
			log.Printf("unable to parse commit date: %s", err)

			continue
		}

		body, err := bodyParser(h, repo)

		if err != nil {
			log.Printf("unable to parse commit body: %s", err)

			continue
		}

		tree, err := treeParser(h, repo)

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
			Project: name,
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

func treeParser(h string, repo string) ([]object, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "--format=%(objectname) %(path)", h)
	cmd.Dir = repo

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

func diffStatParser(h, parent string, repo string) (string, error) {
	cmd := exec.Command("git", "diff", "--stat", fmt.Sprintf("%s..%s", parent, h))
	cmd.Dir = repo

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

func bodyParser(h string, repo string) (string, error) {
	// Because the commit message body is multiline and is tripping the scanner.
	cmd := exec.Command("git", "show", "--no-patch", "--format=%B", h)
	cmd.Dir = repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), nil
}
