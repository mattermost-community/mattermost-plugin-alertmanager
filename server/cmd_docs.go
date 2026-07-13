package main

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// Mattermost's default post length limit is around 16k. Cap doc output
// well below that so the "...truncated..." footer fits and the post
// pipeline has overhead headroom.
const maxDocPostBytes = 14000

// docTopics maps the slash-command argument (lowercase, hyphenless) to
// the embedded filename. Explicit map so we can rename embedded files
// without breaking the user-facing argument vocabulary.
var docTopics = map[string]string{
	"alerts":         "ALERT_CATALOG.md",
	"alert-catalog":  "ALERT_CATALOG.md",
	"requirements":   "ALERT_REQUIREMENTS.md",
	"architecture":   "ARCHITECTURE.md",
	"configuration":  "CONFIGURATION.md",
	"development":    "DEVELOPMENT.md",
	"kubernetes":     "KUBERNETES.md",
	"rotation":       "ROTATION.md",
	"slash_commands": "SLASH_COMMANDS.md",
	"slash-commands": "SLASH_COMMANDS.md",
}

// handleDocs serves embedded docs from inside Mattermost. With no
// argument, it lists available topics. With a topic, it prints the body
// (truncated with a GitHub link if it exceeds maxDocPostBytes).
func (p *Plugin) handleDocs(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return listDocTopics(), nil
	}

	topic := strings.ToLower(fields[2])
	filename, ok := docTopics[topic]
	if !ok {
		return fmt.Sprintf("Unknown docs topic %q. %s", topic, listDocTopics()), nil
	}

	body, err := root.DocsFS.ReadFile("docs/" + filename)
	if err != nil {
		return "", fmt.Errorf("read embedded doc %q: %w", filename, err)
	}

	githubURL := fmt.Sprintf("%s/blob/main/docs/%s", root.RepoURL, filename)
	if len(body) <= maxDocPostBytes {
		return fmt.Sprintf("**%s** — [view on GitHub](%s)\n\n%s", filename, githubURL, string(body)), nil
	}
	truncated := body[:maxDocPostBytes]
	return fmt.Sprintf("**%s** — [view on GitHub](%s)\n\n%s\n\n_...truncated at %d bytes. Read the rest at the GitHub link above._",
		filename, githubURL, string(truncated), maxDocPostBytes), nil
}

// listDocTopics enumerates the embedded docs by walking the embed.FS.
// Dynamic walk so unregistered files surface as missing entries rather
// than silently disappearing.
func listDocTopics() string {
	var embeddedFiles []string
	_ = fs.WalkDir(root.DocsFS, "docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			embeddedFiles = append(embeddedFiles, strings.TrimPrefix(path, "docs/"))
		}
		return nil
	})
	sort.Strings(embeddedFiles)

	topicByFile := make(map[string]string, len(docTopics))
	for topic, file := range docTopics {
		// Prefer snake_case display when both variants register the
		// same file (so the user sees `slash_commands` not
		// `slash-commands` first).
		if existing, ok := topicByFile[file]; ok && !strings.Contains(existing, "-") {
			continue
		}
		topicByFile[file] = topic
	}

	var b strings.Builder
	b.WriteString("**Available docs topics:**\n\n")
	b.WriteString("| Topic | File | View on GitHub |\n")
	b.WriteString("|-------|------|----------------|\n")
	for _, file := range embeddedFiles {
		topic := topicByFile[file]
		if topic == "" {
			topic = "(unregistered)"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `docs/%s` | [link](%s/blob/main/docs/%s) |\n",
			topic, file, root.RepoURL, file))
	}
	b.WriteString("\nUsage: `/alertmanager docs <topic>`")
	return b.String()
}
