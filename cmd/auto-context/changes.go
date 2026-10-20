package main

import (
	"fmt"
	"io"
)

// runChanges prints Project.WriteChanges for one unit.
func runChanges(args []string, stdout, stderr io.Writer, cwd string) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	p, err := loadRepo(stderr, cwd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := p.WriteChanges(stdout, normUnit(args[0])); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
