package core

import "flag"

func startupArguments(executable, data string) []string {
	args := []string{executable, "-data", data}
	headless := headlessBuild
	if f := flag.Lookup("nogui"); f != nil && f.Value.String() == "true" {
		headless = true
	}
	if headless {
		args = append(args, "-nogui")
	}
	return args
}
