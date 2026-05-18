package main

import "os"

var colourEnabled bool

func initColour(noColour bool) {
	colourEnabled = !noColour && isTTY() && os.Getenv("NO_COLOR") == ""
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func green(s string) string {
	if colourEnabled {
		return "\033[32m" + s + "\033[0m"
	}
	return s
}

func red(s string) string {
	if colourEnabled {
		return "\033[31m" + s + "\033[0m"
	}
	return s
}

func yellow(s string) string {
	if colourEnabled {
		return "\033[33m" + s + "\033[0m"
	}
	return s
}

func bold(s string) string {
	if colourEnabled {
		return "\033[1m" + s + "\033[0m"
	}
	return s
}

func dim(s string) string {
	if colourEnabled {
		return "\033[2m" + s + "\033[0m"
	}
	return s
}
