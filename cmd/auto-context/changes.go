package main

import (
	"context"
	"fmt"
	"io"

	"auto-context/internal/project"
)

// runChanges prints Project.WriteChanges for one unit. It needs no hash cache.
func runChanges(ctx context.Context, args []string, stdout, stderr io.Writer, cwd string) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	p, warns, err := loadRepo(ctx, cwd)
	if err != nil {
		return openErr(stderr, cwd, err)
	}
	printWarnings(stderr, warns)
	unit := normUnit(args[0])
	switch {
	case p.ConfigBroken():
		fmt.Fprintln(stderr, project.ErrConfigBroken) // the units would be the defaults
		return exitError
	case !p.Tree().IsUnit(unit):
		fmt.Fprintln(stderr, "unknown unit "+shownName(unit))
		return exitError
	}
	// Git failures are notes in the report; an error here is a failed write.
	if err := p.WriteChanges(stdout, unit); err != nil {
		fmt.Fprintf(stderr, "the report could not be written: %s\n", errText(p.Root(), err, "changes"))
		return exitError
	}
	return exitOK
}
