package main

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

// Helps target file specific diff blocks.
var diffanchor = regexp.MustCompile(`b\/(.*?)$`)

// Match diff body @@ del, ins line numbers.
var aline = regexp.MustCompile(`\-(.*?),`)
var bline = regexp.MustCompile(`\+(.*?),`)

// Match diff body keywords.
var xline = regexp.MustCompile(`^(deleted|index|new|rename|similarity)`)

// Helps decide if value contained in slice.
// https://stackoverflow.com/questions/38654383/how-to-search-for-an-element-in-a-golang-slice
func contains(s []string, n string) bool {
	for _, v := range s {
		if v == n {
			return true
		}
	}

	return false
}

// Helps clear duplicates in slice.
// https://stackoverflow.com/questions/66643946/how-to-remove-duplicates-strings-or-int-from-slice-in-go
func dedupe(input []string) []string {
	set := make(map[string]void)
	list := []string{}

	for _, v := range input {
		if _, ok := set[v]; !ok {
			set[v] = void{}
			list = append(list, v)
		}
	}

	return list
}

func diffbodyparser(d diff) template.HTML {
	var results []string
	feed := strings.Split(strings.TrimSuffix(template.HTMLEscapeString(d.Body), "\n"), "\n")

	var a, b string

	for _, line := range feed {
		if strings.HasPrefix(line, "diff") {
			line = diffanchor.ReplaceAllString(line, `b/<a id="$1">$1</a>`)
			line = fmt.Sprintf("<strong>%s</strong>", line)
		}

		line = xline.ReplaceAllString(line, "<em>$1</em>")

		if strings.HasPrefix(line, "@@") {
			if a != "" && !strings.HasPrefix(a, "---") {
				repl := fmt.Sprintf(`<a href="commit/%s/%s.html#L$1">-$1</a>,`, d.Parent, a)
				line = aline.ReplaceAllString(line, repl)
			}

			if b != "" && !strings.HasPrefix(b, "+++") {
				repl := fmt.Sprintf(`<a href="commit/%s/%s.html#L$1">+$1</a>,`, d.Commit.Hash, b)
				line = bline.ReplaceAllString(line, repl)
			}
		}

		if strings.HasPrefix(line, "---") {
			a = strings.TrimPrefix(line, "--- a/")
			line = fmt.Sprintf("<mark>%s</mark>", line)
		} else if strings.HasPrefix(line, "-") {
			line = fmt.Sprintf("<del>%s</del>", line)
		}

		if strings.HasPrefix(line, "+++") {
			b = strings.TrimPrefix(line, "+++ b/")
			line = fmt.Sprintf("<mark>%s</mark>", line)
		} else if strings.HasPrefix(line, "+") {
			line = fmt.Sprintf("<ins>%s</ins>", line)
		}

		results = append(results, line)
	}

	return template.HTML(strings.Join(results, "\n"))
}

func diffstatbodyparser(o overview) template.HTML {
	var results []string
	feed := strings.Split(strings.TrimSuffix(o.Body, "\n"), "\n")

	for i, line := range feed {
		if i < len(feed)-1 {
			// Link files to corresponding diff.
			columns := strings.Split(line, "|")
			files := strings.Split(columns[0], "=>")

			a := strings.TrimSpace(files[len(files)-1])
			b := fmt.Sprintf(`<a href="commit/%s/diff-%s.html#%s">%s</a>`, o.Hash, o.Parent, a, a)
			l := strings.LastIndex(line, a)

			line = line[:l] + strings.Replace(line[l:], a, b, 1)
		}

		results = append(results, line)
	}

	return template.HTML(strings.Join(results, "\n"))
}
