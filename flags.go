package main

import "strings"

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
