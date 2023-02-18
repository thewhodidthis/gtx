package main

import (
	"strings"
	"testing"
)

func TestContains(t *testing.T) {
	words := strings.Fields("Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.")

	for _, w := range words {
		match := contains(words, w)

		if !match {
			t.Errorf("failed to match %v", w)
			t.Fail()
		}
	}
}

func TestDedupe(t *testing.T) {
	dupes := []string{"one", "one", "two", "three", "three", "three"}

	if len(dedupe(dupes)) != 3 {
		t.Fail()
	}
}
