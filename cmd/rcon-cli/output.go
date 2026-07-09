package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// OutputFormat controls how results are rendered.
type OutputFormat string

const (
	FmtHuman OutputFormat = "human"
	FmtJSON  OutputFormat = "json"
)

// Printer formats and writes output.
type Printer struct {
	w      io.Writer
	format OutputFormat
	color  bool
	tw     *tabwriter.Writer
}

// JSONOutput is a machine-readable response envelope.
type JSONOutput struct {
	OK      bool        `json:"ok"`
	Command string      `json:"command,omitempty"`
	Elapsed string      `json:"elapsed,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func newPrinter(format OutputFormat, color string) *Printer {
	w := os.Stdout
	useColor := false
	switch color {
	case "always":
		useColor = true
	case "never":
		useColor = false
	default: // auto
		fi, err := os.Stdout.Stat()
		useColor = err == nil && (fi.Mode()&os.ModeCharDevice) != 0
	}
	p := &Printer{w: w, format: format, color: useColor}
	if format == FmtHuman {
		p.tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	}
	return p
}

func (p *Printer) Flush() {
	if p.tw != nil {
		p.tw.Flush()
	}
}

// --- Color helpers ---

func (p *Printer) green(s string) string  { return colorize(p.color, "\x1b[32m", s) }
func (p *Printer) red(s string) string    { return colorize(p.color, "\x1b[31m", s) }
func (p *Printer) yellow(s string) string { return colorize(p.color, "\x1b[33m", s) }
func (p *Printer) cyan(s string) string   { return colorize(p.color, "\x1b[36m", s) }
func (p *Printer) dim(s string) string    { return colorize(p.color, "\x1b[2m", s) }
func (p *Printer) bold(s string) string   { return colorize(p.color, "\x1b[1m", s) }
func (p *Printer) greenBold(s string) string {
	return colorize(p.color, "\x1b[1;32m", s)
}
func (p *Printer) redBold(s string) string {
	return colorize(p.color, "\x1b[1;31m", s)
}
func (p *Printer) yellowBold(s string) string {
	return colorize(p.color, "\x1b[1;33m", s)
}

func colorize(enabled bool, code, s string) string {
	if !enabled {
		return s
	}
	return code + s + "\x1b[0m"
}

// --- Output methods ---

// Printf writes formatted output.
func (p *Printer) Printf(format string, args ...interface{}) {
	fmt.Fprintf(p.w, format, args...)
}

// PrintTab writes a tab-separated line for table output.
func (p *Printer) PrintTab(args ...interface{}) {
	if p.tw != nil {
		fmt.Fprint(p.tw, joinTabs(args...))
	} else {
		for i, a := range args {
			if i > 0 {
				fmt.Fprint(p.w, "  ")
			}
			fmt.Fprint(p.w, a)
		}
		fmt.Fprintln(p.w)
	}
}

func joinTabs(args ...interface{}) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	return strings.Join(parts, "\t") + "\t\n"
}

// JSON emits a JSONOutput struct.
func (p *Printer) JSON(v interface{}) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintJSONOk writes a success JSON response.
func (p *Printer) PrintJSONOk(cmd string, elapsed string, data interface{}) {
	p.JSON(JSONOutput{OK: true, Command: cmd, Elapsed: elapsed, Data: data})
}

// PrintJSONErr writes an error JSON response.
func (p *Printer) PrintJSONErr(cmd string, elapsed string, err error) {
	p.JSON(JSONOutput{OK: false, Command: cmd, Elapsed: elapsed, Error: err.Error()})
}

// PrintJSONRaw writes a raw JSON value.
func (p *Printer) PrintJSONRaw(cmd string, elapsed string, rawData string) {
	var parsed interface{}
	if err := json.Unmarshal([]byte(rawData), &parsed); err != nil {
		parsed = rawData // fallback to raw string
	}
	p.PrintJSONOk(cmd, elapsed, parsed)
}
