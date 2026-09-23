package app

import (
	"flag"
	"os"
	"strings"
)

// flagSet creates a named flag.FlagSet that prints errors instead of exiting.
func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// looksLikeFlag reports whether s is shaped like a flag ("-x", "--x")
// rather than a positional value. Commands that read a mandatory
// leading positional (a name, ref, or slug) before parsing flags must
// check this first: without it, a bare "--help" as that first argument
// is silently consumed as the positional's value instead of ever
// reaching the flag parser — for a creating command like `add task`,
// that means it INSERTS a real row named "--help" instead of printing
// help text. See the commands calling this for the guard in place.
func looksLikeFlag(s string) bool {
	return strings.HasPrefix(s, "-")
}

// stringSliceFlag implements flag.Value to collect repeated -tag flags
// into a slice, in the order given.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}
