package main

import (
	"fmt"
	"strings"
)

// cmdDiscover handles the discover mode: list, search, or detail.
//
// Usage:
//
//	rcon-cli discover              → grouped list of all commands
//	rcon-cli discover <query>       → fuzzy search
//	rcon-cli discover --detail <cmd> → parameter details
func cmdDiscover(p *Printer, query string, detailCmd string) {
	loadMeta()

	if p.format == FmtJSON {
		if detailCmd != "" {
			cmd := lookupCommand(detailCmd)
			if cmd == nil {
				p.PrintJSONErr(detailCmd, "", fmt.Errorf("command not found: %s", detailCmd))
				return
			}
			p.PrintJSONOk(detailCmd, "", cmd)
			return
		}
		if query != "" {
			results := searchCommands(query)
			p.PrintJSONOk(query, "", mapResults(results))
			return
		}
		p.PrintJSONOk("list", "", mapAll())
		return
	}

	if detailCmd != "" {
		showDetail(p, detailCmd)
		return
	}

	if query != "" {
		showSearch(p, query)
		return
	}

	showList(p)
}

func showList(p *Printer) {
	meta := loadMeta()
	cats := defaultCategories()

	// Track categorized commands
	catSet := make(map[string]bool)
	for _, cat := range cats {
		for _, c := range cat.Commands {
			catSet[strings.ToLower(c)] = true
		}
	}

	// Display categorized
	for _, cat := range cats {
		p.Printf("\n%s\n", p.bold(cat.Name))
		for _, cmdName := range cat.Commands {
			cmd := metaByCmd[strings.ToLower(cmdName)]
			if cmd == nil {
				continue
			}
			p.PrintTab("  "+p.cyan(cmd.Command), p.dim(truncateDesc(cmd.Description, 60)))
		}
		p.Flush()
	}

	// Check for uncategorized commands
	var uncategorized []string
	for _, cmd := range meta.Commands {
		if !catSet[strings.ToLower(cmd.Command)] {
			uncategorized = append(uncategorized, cmd.Command)
		}
	}
	if len(uncategorized) > 0 {
		p.Printf("\n%s\n", p.bold("其他"))
		for _, name := range uncategorized {
			cmd := metaByCmd[strings.ToLower(name)]
			if cmd == nil {
				continue
			}
			p.PrintTab("  "+p.cyan(cmd.Command), p.dim(truncateDesc(cmd.Description, 60)))
		}
		p.Flush()
	}
}

func showSearch(p *Printer, query string) {
	results := searchCommands(query)
	if len(results) == 0 {
		p.Printf("  %s \"%s\"\n", p.yellow("No commands matching"), query)
		p.Printf("  %s rcon-cli discover\n", p.dim("Tip: list all with"))
		return
	}

	p.Printf("  %s \"%s\" (%d found)\n\n", p.bold("Results for"), query, len(results))
	for _, cmd := range results {
		p.Printf("  %s", p.cyan(cmd.Command))
		if cmd.FriendlyName != "" && cmd.FriendlyName != cmd.Command {
			p.Printf(" — %s", cmd.FriendlyName)
		}
		p.Printf("\n")
		desc := cmd.Description
		if desc != "" {
			p.Printf("    %s\n", p.dim(desc))
		}
		if len(cmd.Parameters) > 0 {
			var paramNames []string
			for _, param := range cmd.Parameters {
				paramNames = append(paramNames, param.ID)
			}
			p.Printf("    %s: %s\n", p.dim("Params"), p.yellow(strings.Join(paramNames, " ")))
		}
		p.Printf("\n")
	}
	p.Printf("  %s rcon-cli discover --detail <command>\n", p.dim("Tip: see full details with"))
}

func showDetail(p *Printer, cmdName string) {
	cmd := lookupCommand(cmdName)
	if cmd == nil {
		p.Printf("  %s \"%s\"\n", p.red("Command not found:"), cmdName)
		// Suggest similar commands
		results := searchCommands(cmdName)
		if len(results) > 0 && len(results) <= 5 {
			p.Printf("  %s\n", p.dim("Did you mean:"))
			for _, r := range results {
				p.Printf("    %s\n", p.cyan(r.Command))
			}
		}
		return
	}

	catID := categoryFor(cmd.Command)
	catLabel := ""
	for _, cat := range defaultCategories() {
		if cat.ID == catID {
			catLabel = cat.Name
			break
		}
	}

	p.Printf("\n  %s", p.bold(cmd.Command))
	if cmd.FriendlyName != "" {
		p.Printf(" — %s", cmd.FriendlyName)
	}
	p.Printf("\n")
	if catLabel != "" {
		p.Printf("  %s: %s\n", p.dim("Category"), catLabel)
	}
	if cmd.Description != "" {
		p.Printf("  %s: %s\n", p.dim("Description"), cmd.Description)
	}
	p.Printf("\n")

	if len(cmd.Parameters) == 0 {
		p.Printf("  %s\n", p.dim("No parameters required."))
	} else {
		p.Printf("  %s (%d)\n", p.bold("Parameters"), len(cmd.Parameters))
		p.Printf("  %s\n", p.dim("Usage: rcon-cli exec "+cmd.Command+" <arg1> <arg2> ..."))
		p.Printf("\n")
		for _, param := range cmd.Parameters {
			// Show ID; only show Name separately if it differs from ID
			if param.Name != "" && param.Name != param.ID {
				p.Printf("  %s  %s\n", p.cyan(param.ID), param.Name)
			} else {
				p.Printf("  %s\n", p.cyan(param.ID))
			}
			if param.Description != "" {
				p.Printf("    %s\n", p.dim(param.Description))
			}

			// Enum values
			values := param.Values
			if param.Constraints != nil && len(param.Constraints.Values) > 0 {
				values = param.Constraints.Values
			}
			if len(values) > 0 {
				p.Printf("    %s: %s\n", p.dim("Values"), p.yellow(strings.Join(values, ", ")))
			}

			// Min/Max constraints
			if param.Constraints != nil {
				if param.Constraints.Min != 0 || param.Constraints.Max != 0 {
					p.Printf("    %s: %.0f ~ %.0f\n", p.dim("Range"), param.Constraints.Min, param.Constraints.Max)
				}
				if param.Constraints.Hint != "" {
					p.Printf("    %s: %s\n", p.dim("Hint"), param.Constraints.Hint)
				}
			}

			p.Printf("\n")
		}
	}

	p.Printf("  %s rcon-cli exec %s", p.dim("Example:"), cmd.Command)
	for _, param := range cmd.Parameters {
		if len(param.Values) > 0 {
			p.Printf(" %s", param.Values[0])
		} else if param.Constraints != nil && param.Constraints.Min > 0 {
			p.Printf(" %d", int(param.Constraints.Min))
		} else {
			p.Printf(" <%s>", param.ID)
		}
	}
	p.Printf("\n")
}

func mapResults(results []*CommandMeta) []map[string]interface{} {
	var out []map[string]interface{}
	for _, c := range results {
		out = append(out, map[string]interface{}{
			"command":      c.Command,
			"friendlyName": c.FriendlyName,
			"description":  c.Description,
			"paramCount":   len(c.Parameters),
		})
	}
	return out
}

func mapAll() map[string]interface{} {
	meta := loadMeta()
	cats := make(map[string][]string)
	for _, cat := range defaultCategories() {
		cats[cat.Name] = cat.Commands
	}
	return map[string]interface{}{
		"serverName": meta.ServerName,
		"total":      len(meta.Commands),
		"categories": cats,
	}
}
