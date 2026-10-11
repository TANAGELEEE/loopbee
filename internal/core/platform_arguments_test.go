package core

import (
	"flag"
	"reflect"
	"testing"
)

func TestStartupArgumentsPreserveMode(t *testing.T) {
	previous := flag.CommandLine
	defer func() { flag.CommandLine = previous }()
	flag.CommandLine = flag.NewFlagSet("startup-test", flag.ContinueOnError)
	headless := flag.CommandLine.Bool("nogui", false, "")
	for _, requested := range []bool{false, true} {
		*headless = requested
		want := []string{"application path", "-data", "data path"}
		if requested || headlessBuild {
			want = append(want, "-nogui")
		}
		if got := startupArguments("application path", "data path"); !reflect.DeepEqual(got, want) {
			t.Fatalf("headless %v: got %q, want %q", requested, got, want)
		}
	}
}
