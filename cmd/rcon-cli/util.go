package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// envOrDefault returns the environment variable value or a default.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// fatalf prints an error message and exits.
func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// truncateDesc shortens a description to maxRunes, appending "..." if truncated.
// Uses rune-based slicing to avoid breaking multi-byte characters.
func truncateDesc(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-3]) + "..."
}

// prettyPrintJSON attempts to indent and print a JSON string.
func prettyPrintJSON(p *Printer, raw string) {
	var obj interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		p.Printf("  %s\n", raw)
		return
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		p.Printf("  %s\n", raw)
		return
	}
	for _, line := range strings.Split(string(formatted), "\n") {
		if line != "" {
			p.Printf("  %s\n", line)
		}
	}
}
