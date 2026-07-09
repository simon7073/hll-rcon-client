package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"github.com/simon7073/hll-rcon-client/rcon"
)

// cmdExec sends a single RCON command and displays the result.
// Returns an error if the command failed (for batch mode tracking).
//
// Usage:
//
//	rcon-cli exec <command> [args...]
//	rcon-cli exec --dry-run <command> [args...]
//	rcon-cli exec --raw <command> [args...]
func cmdExec(p *Printer, client *rcon.Client, cmd string, args []string, dryRun, raw bool, timeout time.Duration) error {
	contentBody := strings.Join(args, " ")

	if dryRun {
		if p.format == FmtJSON {
			p.PrintJSONOk(cmd, "0s", map[string]string{
				"dryRun": "true",
				"params": contentBody,
			})
		} else {
			p.Printf("  %s %s %s\n", p.yellow("[dry-run]"), p.cyan(cmd), p.dim(contentBody))
		}
		return nil
	}

	// Validate against metadata (unless --raw)
	if !raw {
		if err := validateParams(cmd, args); err != nil {
			if p.format == FmtJSON {
				p.PrintJSONErr(cmd, "0s", err)
				return err
			}
			p.Printf("  %s %s\n", p.yellow("Warning:"), err.Error())
		}
	}

	start := time.Now()
	resp, err := client.Send(cmd, contentBody, timeout)
	elapsed := time.Since(start).Round(time.Millisecond)
	elapsedStr := elapsed.String()

	if err != nil {
		printSendError(p, cmd, elapsedStr, err)
		return err
	}

	// --- Success ---
	if p.format == FmtJSON {
		if resp.ContentBody != "" {
			p.PrintJSONRaw(cmd, elapsedStr, resp.ContentBody)
		} else {
			p.PrintJSONOk(cmd, elapsedStr, map[string]interface{}{
				"statusCode":    int(resp.StatusCode),
				"statusMessage": resp.StatusMessage,
			})
		}
		return nil
	}

	p.Printf("  %s %s %s\n",
		p.greenBold("OK"),
		p.cyan(cmd),
		p.dim("("+elapsedStr+")"))
	if resp.StatusMessage != "" {
		p.Printf("  %s: %s\n", p.dim("Status"), resp.StatusMessage)
	}
	if resp.ContentBody != "" {
		p.Printf("  %s:\n", p.dim("Response"))
		trimmed := strings.TrimSpace(resp.ContentBody)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			prettyPrintJSON(p, resp.ContentBody)
		} else {
			p.Printf("  %s\n", resp.ContentBody)
		}
	}
	return nil
}

// printSendError formats command execution errors with contextual hints.
func printSendError(p *Printer, cmd, elapsed string, err error) {
	if p.format == FmtJSON {
		p.PrintJSONErr(cmd, elapsed, err)
		return
	}

	p.Printf("  %s %s %s\n",
		p.redBold("ERROR"),
		p.cyan(cmd),
		p.dim("("+elapsed+")"))

	var rconErr *core.RconError
	if errors.As(err, &rconErr) && rconErr.StatusCode != 0 {
		// Server returned an error status (400/401/500)
		p.Printf("  Status: %s %s\n",
			p.red(fmt.Sprintf("[%d]", rconErr.StatusCode)),
			p.dim(rconErr.StatusMessage))

		switch rconErr.StatusCode {
		case core.StatusBadRequest:
			p.Printf("  %s %s\n",
				p.dim("Tip: Invalid parameters. Check with:"),
				p.cyan("rcon-cli discover --detail "+cmd))
		case core.StatusUnauthorized:
			p.Printf("  %s\n", p.dim("Tip: Check password / permissions."))
		}
		return
	}

	p.Printf("  %s\n", p.red(err.Error()))

	switch {
	case errors.Is(err, core.ErrMagicMismatch):
		p.Printf("  %s\n", p.dim("Tip: TCP stream corruption. rcon-cli will auto-reconnect and retry."))
	case strings.Contains(err.Error(), "timeout"):
		p.Printf("  %s\n", p.dim("Tip: Increase timeout with --timeout flag."))
	case strings.Contains(err.Error(), "connection refused"):
		p.Printf("  %s\n", p.dim("Tip: Check host/port. Is the HLL server running?"))
	}
}

// validateParams checks if the command args look valid based on metadata.
func validateParams(cmd string, args []string) error {
	meta := lookupCommand(cmd)
	if meta == nil {
		return fmt.Errorf("unknown command %q (use --raw to send anyway)", cmd)
	}

	if len(meta.Parameters) == 0 && len(args) > 0 {
		return fmt.Errorf("command %q expects no parameters, got %d", cmd, len(args))
	}

	// Check for enum values
	for i, param := range meta.Parameters {
		if i >= len(args) {
			break
		}
		values := param.Values
		if param.Constraints != nil && len(param.Constraints.Values) > 0 {
			values = param.Constraints.Values
		}
		if len(values) > 0 {
			found := false
			for _, v := range values {
				if strings.EqualFold(args[i], v) {
					found = true
					break
				}
			}
			if !found && args[i] != "" {
				return fmt.Errorf("%s: %q is not a known value (expected: %s)",
					param.ID, args[i], strings.Join(values, ", "))
			}
		}
	}
	return nil
}
