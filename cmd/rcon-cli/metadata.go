package main

import (
	_ "embed"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

//go:embed commands_meta.json
var defaultMeta []byte

// ParamConstraint represents constraints on a parameter value.
type ParamConstraint struct {
	Values []string `json:"values"`
	Hint   string   `json:"hint"`
	Min    float64  `json:"min"`
	Max    float64  `json:"max"`
}

// ParamMeta describes a single command parameter.
type ParamMeta struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Values      []string        `json:"values"`
	Constraints *ParamConstraint `json:"constraints"`
}

// CommandMeta describes a single RCON command.
type CommandMeta struct {
	Command      string      `json:"command"`
	FriendlyName string      `json:"friendlyName"`
	Description  string      `json:"description"`
	Parameters   []ParamMeta `json:"parameters"`
}

// MetaFile is the top-level structure of commands_meta.json.
type MetaFile struct {
	GeneratedAt string        `json:"generatedAt"`
	ServerName  string        `json:"serverName"`
	Commands    []CommandMeta `json:"commands"`
}

// Category groups commands by functional area.
type Category struct {
	ID       string
	Name     string
	Commands []string
}

var (
	meta      *MetaFile
	metaByCmd map[string]*CommandMeta
)

func loadMeta() *MetaFile {
	if meta != nil {
		return meta
	}
	meta = &MetaFile{}
	data := defaultMeta
	if path := flagMetaFile; path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			fatalf("Failed to read meta file: %v", err)
		}
	}
	if err := json.Unmarshal(data, meta); err != nil {
		fatalf("Failed to parse commands meta: %v", err)
	}
	// Build lookup map
	metaByCmd = make(map[string]*CommandMeta, len(meta.Commands))
	for i := range meta.Commands {
		cmd := &meta.Commands[i]
		metaByCmd[strings.ToLower(cmd.Command)] = cmd
	}
	return meta
}

func lookupCommand(name string) *CommandMeta {
	loadMeta()
	lc := strings.ToLower(name)
	if c, ok := metaByCmd[lc]; ok {
		return c
	}
	// Prefix match: prefer the shortest command name (most specific)
	var best *CommandMeta
	for k, v := range metaByCmd {
		if strings.HasPrefix(k, lc) {
			if best == nil || len(v.Command) < len(best.Command) {
				best = v
			}
		}
	}
	return best
}

// searchCommands returns commands matching the query.
func searchCommands(query string) []*CommandMeta {
	loadMeta()
	q := strings.ToLower(query)
	var results []*CommandMeta
	for i := range meta.Commands {
		cmd := &meta.Commands[i]
		if matchCommand(cmd, q) {
			results = append(results, cmd)
		}
	}
	// Sort: exact match first, then prefix, then substring
	sort.Slice(results, func(i, j int) bool {
		return commandRank(results[i], q) < commandRank(results[j], q)
	})
	return results
}

func matchCommand(cmd *CommandMeta, q string) bool {
	if strings.Contains(strings.ToLower(cmd.Command), q) {
		return true
	}
	if strings.Contains(strings.ToLower(cmd.FriendlyName), q) {
		return true
	}
	if strings.Contains(strings.ToLower(cmd.Description), q) {
		return true
	}
	return false
}

func commandRank(cmd *CommandMeta, q string) int {
	lc := strings.ToLower(cmd.Command)
	if lc == q {
		return 0
	}
	if strings.HasPrefix(lc, q) {
		return 1
	}
	return 2
}

// defaultCategories returns the predefined command categories.
func defaultCategories() []Category {
	return []Category{
		{ID: "player", Name: "玩家管理",
			Commands: []string{
				"KickPlayer", "PermanentBanPlayer", "TemporaryBanPlayer",
				"RemovePermanentBan", "RemoveTemporaryBan", "PunishPlayer",
				"ForceTeamSwitch", "MessagePlayer", "MessageAllPlayers",
			}},
		{ID: "admin", Name: "权限管理",
			Commands: []string{
				"AddAdmin", "RemoveAdmin", "GetAdminUsers",
				"GetAdminGroups", "GetAdminLog", "AddVip", "RemoveVip",
			}},
		{ID: "map", Name: "地图管理",
			Commands: []string{
				"ChangeMap", "AddMapToRotation", "RemoveMapFromRotation",
				"AddMapToSequence", "RemoveMapFromSequence",
				"MoveMapInSequence", "SetSectorLayout",
			}},
		{ID: "server", Name: "服务器控制",
			Commands: []string{
				"ServerBroadcast", "SetWelcomeMessage",
				"SetMaxQueuedPlayers", "SetVipSlotCount",
				"SetDynamicWeatherEnabled", "GetServerChangelist",
			}},
		{ID: "match", Name: "比赛与计时",
			Commands: []string{
				"SetMatchTimer", "RemoveMatchTimer",
				"SetWarmupTimer", "RemoveWarmupTimer",
			}},
		{ID: "config", Name: "游戏设置",
			Commands: []string{
				"SetAutoBalanceEnabled", "GetAutoBalanceEnabled",
				"SetAutoBalanceThreshold", "GetAutoBalanceThreshold",
				"SetHighPingThreshold", "GetHighPingThreshold",
				"SetIdleKickDuration", "GetKickIdleDuration",
				"SetMapShuffleEnabled", "GetMapShuffleEnabled",
				"SetTeamSwitchCooldown", "GetTeamSwitchCooldown",
				"SetVoteKickEnabled", "GetVoteKickEnabled",
				"SetVoteKickThreshold", "GetVoteKickThreshold",
				"ResetVoteKickThreshold",
			}},
		{ID: "filter", Name: "聊天过滤",
			Commands: []string{
				"AddBannedWords", "RemoveBannedWords",
			}},
		{ID: "platoon", Name: "小队管理",
			Commands: []string{
				"DisbandPlatoon", "RemovePlayerFromPlatoon",
			}},
		{ID: "info", Name: "信息查询",
			Commands: []string{
				"GetServerInformation", "GetClientReferenceData",
				"GetDisplayableCommands", "GetPermanentBans",
				"GetTemporaryBans",
			}},
	}
}

// categoryFor returns the category ID for a command, or "" if uncategorized.
func categoryFor(cmdName string) string {
	for _, cat := range defaultCategories() {
		for _, c := range cat.Commands {
			if strings.EqualFold(c, cmdName) {
				return cat.ID
			}
		}
	}
	return ""
}
