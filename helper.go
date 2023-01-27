package main

import (
	"reflect"
)

type void struct{}

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
func dedupe(input []string) []reflect.Value {
	set := make(map[string]void)

	for _, v := range input {
		set[v] = void{}
	}

	return reflect.ValueOf(set).MapKeys()
}
