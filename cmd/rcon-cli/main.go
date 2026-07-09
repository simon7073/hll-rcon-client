package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"github.com/simon7073/hll-rcon-client/rcon"
)

// Flags
var (
	flagHost      string
	flagPort      string
	flagPass      string
	flagTimeout   time.Duration
	flagOutput    string
	flagColor     string
	flagMetaFile  string
	flagHTTPProxy string
)

func init() {
	flagHost      = envOrDefault("RCON_HOST", "127.0.0.1")
	flagPort      = envOrDefault("RCON_PORT", "29017")
	flagPass      = envOrDefault("RCON_PASS", "")
	flagTimeout   = 5 * time.Second
	flagColor     = "auto"
	flagOutput    = "human"
	flagHTTPProxy = "" // 代理只从 --http-proxy flag 读取，不自动读系统环境变量
}

func main() {
	args := os.Args[1:]

	// No arguments → interactive REPL (the default mode)
	if len(args) == 0 {
		startREPL()
		return
	}

	// Help / version short-circuits
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(os.Stdout)
		return
	}
	if args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintln(os.Stdout, "rcon-cli v2.0 (rcon-client)")
		return
	}

	// Single pass: collect flags and positional args.
	// The first positional arg becomes the mode; the rest are mode arguments.
	var positional []string
	rawFlag := false
	dryRunFlag := false
	discoverDetail := ""
	stopOnErrorFlag := true

	valueFlags := map[string]bool{
		"--host": true, "--port": true, "--pass": true,
		"--timeout": true, "--output": true, "--color": true,
		"--meta-file": true, "--detail": true, "--stop-on-error": true,
		"--http-proxy": true,
	}

	i := 0
	for i < len(args) {
		a := args[i]
		if valueFlags[a] {
			val := nextArg(args, &i, a)
			switch a {
			case "--host":
				flagHost = val
			case "--port":
				flagPort = val
			case "--pass":
				flagPass = val
			case "--timeout":
				d, err := time.ParseDuration(val)
				if err != nil {
					fatalf("Invalid --timeout: %v", err)
				}
				flagTimeout = d
			case "--output":
				if val != "human" && val != "json" {
					fatalf("--output must be 'human' or 'json', got: %s", val)
				}
				flagOutput = val
			case "--color":
				if val != "auto" && val != "always" && val != "never" {
					fatalf("--color must be 'auto', 'always', or 'never', got: %s", val)
				}
				flagColor = val
			case "--meta-file":
				flagMetaFile = val
			case "--detail":
				discoverDetail = val
			case "--stop-on-error":
				stopOnErrorFlag = val == "true" || val == "1" || val == "yes"
			case "--http-proxy":
				flagHTTPProxy = val
			}
		} else if a == "--raw" {
			rawFlag = true
		} else if a == "--dry-run" {
			dryRunFlag = true
		} else if strings.HasPrefix(a, "--") {
			fatalf("Unknown flag: %s", a)
		} else {
			positional = append(positional, a)
		}
		i++
	}

	// First positional arg is the mode (empty = implicit REPL)
	var mode string
	var modeArgs []string
	if len(positional) > 0 {
		mode = positional[0]
		modeArgs = positional[1:]
	}

	p := newPrinter(OutputFormat(flagOutput), flagColor)

	switch mode {
	case "ping":
		cmdPing(p, flagHost, flagPort, flagPass, flagTimeout, resolveProxyOpts()...)

	case "exec":
		if len(modeArgs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: rcon-cli exec <command> [args...]")
			os.Exit(1)
		}
		client := rcon.NewClient(flagHost, flagPort, flagPass, resolveProxyOpts()...)
		if err := client.Connect(flagTimeout); err != nil {
			printConnectError(p, "exec", err)
			os.Exit(1)
		}
		defer client.Close()
		if err := cmdExec(p, client, modeArgs[0], modeArgs[1:], dryRunFlag, rawFlag, flagTimeout); err != nil {
			os.Exit(1)
		}

	case "discover":
		query := ""
		if len(modeArgs) > 0 {
			query = modeArgs[0]
		}
		cmdDiscover(p, query, discoverDetail)

	case "batch":
		if len(modeArgs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: rcon-cli batch <file>")
			os.Exit(1)
		}
		client := rcon.NewClient(flagHost, flagPort, flagPass, resolveProxyOpts()...)
		if err := client.Connect(flagTimeout); err != nil {
			printConnectError(p, "batch", err)
			os.Exit(1)
		}
		defer client.Close()
		cmdBatch(p, client, modeArgs[0], stopOnErrorFlag, flagTimeout)

	case "":
		// Implicit REPL (only flags, no mode specified)
		startREPL()

	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n\n", mode)
		usage(os.Stderr)
		os.Exit(1)
	}
}

// resolveProxyOpts 解析代理配置，返回 core.DialOption 列表
//
// 代理仅在用户通过 --http-proxy 显式指定时生效，不自动读取系统环境变量。
// 原始 TCP socket 不应透过 HTTP 代理，除非用户明确需要。
func resolveProxyOpts() []core.DialOption {
	if flagHTTPProxy != "" {
		return []core.DialOption{core.WithHTTPProxy(flagHTTPProxy)}
	}
	return nil
}

// startREPL creates a client and enters interactive mode.
func startREPL() {
	p := newPrinter(OutputFormat(flagOutput), flagColor)
	client := rcon.NewClient(flagHost, flagPort, flagPass, resolveProxyOpts()...)
	if err := client.Connect(flagTimeout); err != nil {
		printConnectError(p, "repl", err)
		os.Exit(1)
	}
	defer client.Close()
	cmdREPL(p, client, flagTimeout)
}

// printConnectError formats a connection failure consistently across modes.
func printConnectError(p *Printer, mode string, err error) {
	if p.format == FmtJSON {
		p.PrintJSONErr(mode, "0s", fmt.Errorf("connect: %w", err))
	} else {
		p.Printf("  %s %v\n", p.redBold("CONNECT FAILED"), err)
	}
}

// nextArg returns the value following a flag and advances i past it.
// Exits with a clear message if the flag is missing its value.
func nextArg(args []string, i *int, flagName string) string {
	*i++
	if *i >= len(args) {
		fatalf("Flag %s requires a value", flagName)
	}
	return args[*i]
}

func usage(w *os.File) {
	fmt.Fprint(w, `rcon-cli - HLL RCON command-line tool

Usage:
  rcon-cli                               Interactive REPL (default)
  rcon-cli ping                          Test connectivity
  rcon-cli discover [query]              Browse / search commands
  rcon-cli discover --detail <cmd>       Show command parameter details
  rcon-cli exec <command> [args...]      Execute a single command
  rcon-cli batch <file>                  Execute commands from file
  rcon-cli help                          Show this help

Global flags:
  --host <addr>        Server address   (env: RCON_HOST, default: 127.0.0.1)
  --port <port>        Server port      (env: RCON_PORT, default: 29017)
  --pass <password>    RCON password    (env: RCON_PASS)
  --http-proxy <addr>  HTTP CONNECT proxy (显式指定才生效，不自动读系统代理)
  --timeout <dur>      Command timeout  (default: 5s)
  --output <fmt>       Output format: human | json (default: human)
  --color <mode>       Color mode: auto | always | never (default: auto)
  --meta-file <path>   Override commands_meta.json

Exec flags:
  --dry-run     Preview without executing
  --raw         Skip metadata validation

Batch flags:
  --stop-on-error <bool>  Stop on first error (default: true)

Examples:
  rcon-cli                              # Start interactive REPL
  rcon-cli ping --host your-server-ip    # Test connectivity
  rcon-cli discover                     # List all commands
  rcon-cli discover ban                 # Search for commands
  rcon-cli discover --detail KickPlayer # Show parameter details
  rcon-cli exec players                 # Get player list
  rcon-cli exec KickPlayer "Name" "TK"  # Kick player
  rcon-cli exec --output json session   # JSON output
  rcon-cli batch commands.txt           # Execute batch file
`)
}
