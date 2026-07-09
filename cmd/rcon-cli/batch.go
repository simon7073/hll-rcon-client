package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/simon7073/hll-rcon-client/rcon"
)

// cmdBatch executes commands from a file or stdin.
//
// Usage:
//
//	rcon-cli batch <file>          → execute commands from file
//	rcon-cli batch -               → execute commands from stdin
//
// File format: one command per line, whitespace-trimmed.
// Lines starting with # are comments (printed as section headers in human mode).
// Blank lines are skipped.
func cmdBatch(p *Printer, client *rcon.Client, filePath string, stopOnError bool, timeout time.Duration) {
	var scanner *bufio.Scanner

	if filePath == "-" {
		scanner = newScanner(os.Stdin)
	} else {
		f, err := os.Open(filePath)
		if err != nil {
			if p.format == FmtJSON {
				p.PrintJSONErr("batch", "0s", fmt.Errorf("open file: %w", err))
			} else {
				p.Printf("  %s %v\n", p.redBold("ERROR"), err)
			}
			os.Exit(1)
		}
		defer f.Close()
		scanner = newScanner(f)
	}

	var results []batchResult
	lineNo := 0
	humanErrors := 0

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and blanks
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if p.format != FmtJSON {
				p.Printf("\n%s\n", p.bold(strings.TrimPrefix(line, "#")))
			}
			continue
		}

		// Parse command: first token is command, rest is params
		parts := parseArgs(line)
		if len(parts) == 0 {
			continue
		}
		cmd := parts[0]
		params := parts[1:]

		if p.format == FmtJSON {
			start := time.Now()
			resp, err := client.Send(cmd, strings.Join(params, " "), timeout)
			elapsed := time.Since(start).Round(time.Millisecond)

			r := batchResult{
				Line:    lineNo,
				Command: cmd,
				Elapsed: elapsed.String(),
				Success: err == nil,
			}
			if err != nil {
				r.Error = err.Error()
			} else if resp.ContentBody != "" {
				r.Body = resp.ContentBody
			}
			results = append(results, r)

			if stopOnError && err != nil {
				break
			}
		} else {
			p.Printf("  [%d] %s %s\n", lineNo, p.cyan(cmd), p.dim(strings.Join(params, " ")))
			err := cmdExec(p, client, cmd, params, false, false, timeout)
			if err != nil {
				humanErrors++
				if stopOnError {
					p.Printf("\n  %s Stopped at line %d.\n", p.redBold("STOP"), lineNo)
					break
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		p.Printf("  %s %v\n", p.redBold("READ ERROR"), err)
	}

	if p.format == FmtJSON {
		var successCount, failCount int
		for _, r := range results {
			if r.Success {
				successCount++
			} else {
				failCount++
			}
		}
		p.JSON(map[string]interface{}{
			"ok":      failCount == 0,
			"total":   len(results),
			"success": successCount,
			"failed":  failCount,
			"results": results,
		})
		return
	}

	if humanErrors > 0 {
		p.Printf("\n  %s  %d error(s)\n", p.redBold("DONE WITH ERRORS"), humanErrors)
	} else {
		p.Printf("\n  %s\n", p.greenBold("DONE"))
	}
}

type batchResult struct {
	Line    int    `json:"line"`
	Command string `json:"command"`
	Elapsed string `json:"elapsed"`
	Success bool   `json:"success"`
	Body    string `json:"body,omitempty"`
	Error   string `json:"error,omitempty"`
}
