package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/simon7073/hll-rcon-client/rcon"
)

// cmdREPL starts an interactive read-eval-print loop.
func cmdREPL(p *Printer, client *rcon.Client, timeout time.Duration) {
	loadMeta()

	p.Printf("%s\n", p.bold("rcon-cli interactive mode"))
	p.Printf("  Server: %s\n", p.cyan(client.Addr()))
	p.Printf("  Commands: %s\n", p.dim(fmt.Sprintf("%d loaded", len(meta.Commands))))
	p.Printf("  Meta commands: %s\n", p.dim(":help, :list, :search <q>, :detail <cmd>, :ping, :exit"))
	p.Printf("  %s\n", p.dim("Type a command name + args, or Ctrl+C / :exit to quit."))
	p.Printf("\n")

	scanner := newScanner(os.Stdin)

	for {
		p.Printf("%s ", p.greenBold("rcon>"))
		if !scanner.Scan() {
			p.Printf("\n")
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Meta commands
		if strings.HasPrefix(line, ":") {
			if handleMeta(p, client, scanner, line, timeout) {
				break
			}
			continue
		}

		// Parse: first token = command, rest = args
		parts := parseArgs(line)
		if len(parts) == 0 {
			continue
		}
		cmd := parts[0]
		args := parts[1:]

		// Auto-correct casing via metadata
		if m := lookupCommand(cmd); m != nil {
			cmd = m.Command
		}

		// Danger confirmation for destructive commands
		if isDangerous(cmd) && !confirm(p, scanner, cmd, args) {
			continue
		}

		cmdExec(p, client, cmd, args, false, false, timeout)
		p.Printf("\n")
	}

	p.Printf("%s\n", p.dim("Goodbye."))
}

// handleMeta processes REPL meta commands (prefixed with :).
// Returns true if the REPL should exit.
func handleMeta(p *Printer, client *rcon.Client, scanner *bufio.Scanner, line string, timeout time.Duration) bool {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false
	}
	metaCmd := strings.TrimPrefix(parts[0], ":")

	switch metaCmd {
	case "help", "h":
		p.Printf("\n")
		p.Printf("  %s\n", p.bold("Meta Commands"))
		p.Printf("  %s  %s\n", p.cyan(":help, :h"), p.dim("Show this help"))
		p.Printf("  %s  %s\n", p.cyan(":list, :l"), p.dim("List all commands by category"))
		p.Printf("  %s  %s\n", p.cyan(":search <q>"), p.dim("Search commands by name/description"))
		p.Printf("  %s  %s\n", p.cyan(":detail <cmd>"), p.dim("Show parameter details for a command"))
		p.Printf("  %s  %s\n", p.cyan(":ping"), p.dim("Test connection latency"))
		p.Printf("  %s  %s\n", p.cyan(":exit, :quit"), p.dim("Exit the REPL"))
		p.Printf("\n")
		p.Printf("  %s\n", p.bold("Usage"))
		p.Printf("  %s\n", p.dim("  <command> <arg1> <arg2> ..."))
		p.Printf("  %s\n", p.dim("  Use quotes for multi-word values: KickPlayer \"Player Name\" \"Reason\""))
		p.Printf("  %s\n", p.dim("  Type Ctrl+C or :exit to quit."))
		p.Printf("\n")

	case "list", "l":
		showList(p)
		p.Printf("\n")

	case "search", "s":
		query := strings.Join(parts[1:], " ")
		if query == "" {
			p.Printf("  %s :search <query>\n", p.dim("Usage:"))
			return false
		}
		showSearch(p, query)
		p.Printf("\n")

	case "detail", "d":
		if len(parts) < 2 {
			p.Printf("  %s :detail <command>\n", p.dim("Usage:"))
			return false
		}
		showDetail(p, parts[1])
		p.Printf("\n")

	case "ping":
		// Lightweight connectivity check — don't reconnect if already connected
		if !client.IsClosed() {
			p.Printf("  %s %s\n", p.greenBold("OK"), p.dim("(already connected)"))
		} else {
			p.Printf("  %s %s...\n", p.dim("Reconnecting to"), client.Addr())
			start := time.Now()
			err := client.Connect(timeout)
			elapsed := time.Since(start).Round(time.Millisecond)
			if err != nil {
				p.Printf("  %s %s - %v\n",
					p.redBold("FAIL"), p.dim("("+elapsed.String()+")"), p.red(err.Error()))
			} else {
				p.Printf("  %s %s\n",
					p.greenBold("OK"), p.dim("("+elapsed.String()+")"))
			}
		}
		p.Printf("\n")

	case "exit", "quit", "q":
		return true

	default:
		p.Printf("  %s :%s\n", p.yellow("Unknown meta command:"), metaCmd)
		p.Printf("  %s\n", p.dim("Use :help to see available commands."))
	}
	return false
}

// isDangerous returns true for commands that modify server state.
func isDangerous(cmd string) bool {
	dangerous := map[string]bool{
		"KickPlayer":          true,
		"PermanentBanPlayer":  true,
		"TemporaryBanPlayer":  true,
		"PunishPlayer":        true,
		"ForceTeamSwitch":     true,
		"ServerBroadcast":     true,
		"Broadcast":           true,
		"MessageAllPlayers":   true,
		"ChangeMap":           true,
		"ShutdownServer":      true,
		"RestartMatch":        true,
		"AddAdmin":            true,
		"RemoveAdmin":         true,
		"DisbandPlatoon":      true,
	}
	return dangerous[cmd]
}

// confirm asks for confirmation before executing a dangerous command.
// Uses the same scanner as the REPL loop to avoid consuming extra input.
func confirm(p *Printer, scanner *bufio.Scanner, cmd string, args []string) bool {
	p.Printf("  %s\n", p.yellowBold("DANGER: This command modifies server state."))
	p.Printf("  Command: %s\n", p.cyan(cmd))
	if len(args) > 0 {
		p.Printf("  Args: %s\n", p.dim(strings.Join(args, " ")))
	}
	p.Printf("  %s [y/N]: ", p.yellow("Confirm"))

	if scanner.Scan() {
		resp := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return resp == "y" || resp == "yes"
	}
	return false
}

// parseArgs splits a line into arguments, respecting double-quoted strings.
// Handles backslash escapes inside quotes.
func parseArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && inQuotes && i+1 < len(line):
			// Escape next character inside quotes
			current.WriteByte(line[i+1])
			i++
		case c == '"':
			inQuotes = !inQuotes
		case c == ' ' && !inQuotes:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// newScanner creates a bufio.Scanner with an enlarged buffer for long pasted input.
func newScanner(r *os.File) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB per line
	return s
}
