package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

const (
	maxImagePrefixes     = 40
	maxImagePrefixLength = 200
	// imageAllowlistMarker is the digest alternative at the end of the shipped
	// UnexpectedContainerImage regex. Extras are inserted in front of it so a
	// sha256 image stays allowed and the PromQL string stays closed.
	imageAllowlistMarker = `|sha256:).*"`
)

// registryPrefixPattern is the character set of a registry prefix. Dots are
// allowed. Regex metacharacters are not, so a prefix cannot close the PromQL
// string or add another alternative.
var registryPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*$`)

// imageAllowlistExpr finds the shipped alternation inside the sample rule.
var imageAllowlistExpr = regexp.MustCompile(`image!~"\^\(([^"]+)\)\.\*"`)

// handleAllowlist lists, adds, or removes extra registry prefixes for the
// UnexpectedContainerImage rule. Sysadmin only. The list is a System Console
// setting. It changes the next `/alertmanager format rules crd` file.
// Prometheus keeps evaluating the rule that was last applied.
func (p *Plugin) handleAllowlist(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	action := ""
	prefix := ""
	if len(fields) >= 3 {
		action = strings.ToLower(fields[2])
	}
	if len(fields) >= 4 {
		prefix = fields[3]
	}
	if len(fields) > 4 {
		return "A registry prefix cannot contain spaces. Usage: `/alertmanager admin allowlist add ghcr.io/myorg/`", nil
	}

	current, err := p.loadImageAllowlist()
	if err != nil {
		return fmt.Sprintf(":warning: Could not read the image allowlist: %v", err), nil
	}

	switch action {
	case "", "list":
		return p.formatImageAllowlist(current), nil
	case "add":
		if prefix == "" {
			return "Usage: `/alertmanager admin allowlist add <prefix>`\n\nExample: `/alertmanager admin allowlist add ghcr.io/myorg/`", nil
		}
		normalized, nerr := normalizeRegistryPrefix(prefix)
		if nerr != nil {
			return ":warning: " + nerr.Error(), nil
		}
		shipped, serr := p.shippedImagePrefixes()
		if serr != nil {
			return fmt.Sprintf(":warning: Could not read the shipped rule: %v", serr), nil
		}
		if containsPrefix(shipped, normalized) {
			return fmt.Sprintf("`%s` is already in the shipped rule.", normalized), nil
		}
		if containsPrefix(current, normalized) {
			return fmt.Sprintf("`%s` is already in the extra list.", normalized), nil
		}
		if len(current) >= maxImagePrefixes {
			return fmt.Sprintf(":warning: The extra list is at its cap of %d prefixes. Remove one first.", maxImagePrefixes), nil
		}
		next := append(append([]string{}, current...), normalized)
		if serr := p.saveImageAllowlist(next); serr != nil {
			return fmt.Sprintf(":warning: Could not save the allowlist: %v", serr), nil
		}
		p.auditLog("allowlist.add", args.UserId, normalized, args.ChannelId, "success")
		return fmt.Sprintf("Added `%s`.\n\n%s", normalized, p.reapplyAllowlistNote()), nil
	case "remove":
		if prefix == "" {
			return "Usage: `/alertmanager admin allowlist remove <prefix>`", nil
		}
		normalized, nerr := normalizeRegistryPrefix(prefix)
		if nerr != nil {
			return ":warning: " + nerr.Error(), nil
		}
		next := make([]string, 0, len(current))
		found := false
		for _, item := range current {
			if item == normalized {
				found = true
				continue
			}
			next = append(next, item)
		}
		if !found {
			return fmt.Sprintf("`%s` is not in the extra list. The shipped prefixes cannot be removed here.", normalized), nil
		}
		if serr := p.saveImageAllowlist(next); serr != nil {
			return fmt.Sprintf(":warning: Could not save the allowlist: %v", serr), nil
		}
		p.auditLog("allowlist.remove", args.UserId, normalized, args.ChannelId, "success")
		return fmt.Sprintf("Removed `%s`.\n\n%s", normalized, p.reapplyAllowlistNote()), nil
	default:
		return "Usage: `/alertmanager admin allowlist`, `/alertmanager admin allowlist add <prefix>`, or `/alertmanager admin allowlist remove <prefix>`", nil
	}
}

func (p *Plugin) reapplyAllowlistNote() string {
	return "This does not change a running PrometheusRule. Run `/alertmanager format rules crd` and apply that file. Until then Prometheus still uses the previous regex."
}

func (p *Plugin) formatImageAllowlist(extras []string) string {
	var b strings.Builder
	b.WriteString("**UnexpectedContainerImage allowlist**\n\n")
	shipped, err := p.shippedImagePrefixes()
	if err != nil {
		fmt.Fprintf(&b, ":warning: Could not read the shipped prefixes: %v\n\n", err)
	} else {
		b.WriteString("Shipped with the rule:\n")
		for _, item := range shipped {
			fmt.Fprintf(&b, "- `%s`\n", item)
		}
		b.WriteString("\n")
	}
	if len(extras) == 0 {
		b.WriteString("No extra prefixes. Add one with `/alertmanager admin allowlist add ghcr.io/myorg/`.\n")
	} else {
		b.WriteString("Extra prefixes, included in the next `format rules crd` file:\n")
		for _, item := range extras {
			fmt.Fprintf(&b, "- `%s`\n", item)
		}
	}
	b.WriteString("\n")
	b.WriteString(p.reapplyAllowlistNote())
	return b.String()
}

func (p *Plugin) loadImageAllowlist() ([]string, error) {
	var raw rawConfiguration
	if err := p.API.LoadPluginConfiguration(&raw); err != nil {
		return nil, err
	}
	return parseImageAllowlist(raw.ImageRegistryAllowlist)
}

func (p *Plugin) saveImageAllowlist(prefixes []string) error {
	var raw rawConfiguration
	if err := p.API.LoadPluginConfiguration(&raw); err != nil {
		return fmt.Errorf("load current plugin configuration: %w", err)
	}
	cfg := raw.toConfigMap()
	cfg["imageregistryallowlist"] = strings.Join(prefixes, "\n")
	if err := p.client.Configuration.SavePluginConfig(cfg); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) shippedImagePrefixes() ([]string, error) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		return nil, err
	}
	return parseShippedImagePrefixes(string(raw))
}

// parseImageAllowlist splits the System Console value. Blank lines are ignored.
// Each entry is normalized so a hand-edited setting and `allowlist add` agree.
func parseImageAllowlist(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	}) {
		normalized, err := normalizeRegistryPrefix(line)
		if err != nil {
			return nil, fmt.Errorf("image allowlist: %w", err)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if len(out) > maxImagePrefixes {
			return nil, fmt.Errorf("image allowlist has more than %d prefixes", maxImagePrefixes)
		}
	}
	return out, nil
}

// normalizeRegistryPrefix lowercases a registry prefix and adds a trailing
// slash when the caller did not end it with `/` or `:`. The slash keeps
// `ghcr.io/myorg` from also matching `ghcr.io/myorg-evil`.
func normalizeRegistryPrefix(in string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(in))
	if s == "" {
		return "", fmt.Errorf("prefix is empty")
	}
	if len(s) > maxImagePrefixLength {
		return "", fmt.Errorf("prefix %q is longer than %d characters", in, maxImagePrefixLength)
	}
	if !registryPrefixPattern.MatchString(s) {
		return "", fmt.Errorf("prefix %q may contain only letters, digits, '.', '_', ':', '/', and '-'", in)
	}
	if !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ":") {
		s += "/"
	}
	return s, nil
}

func containsPrefix(list []string, prefix string) bool {
	return slices.Contains(list, prefix)
}

// parseShippedImagePrefixes reads the alternation baked into the sample rule
// and unescapes the PromQL string spelling (`\\.` is a literal dot).
func parseShippedImagePrefixes(rawYAML string) ([]string, error) {
	m := imageAllowlistExpr.FindStringSubmatch(rawYAML)
	if m == nil {
		return nil, fmt.Errorf("UnexpectedContainerImage allowlist not found in the sample rule")
	}
	parts := strings.Split(m[1], "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.ReplaceAll(part, `\\.`, `.`))
	}
	return out, nil
}

// promQLRegexLiteral escapes a prefix for the double-quoted PromQL string in
// the sample rule. QuoteMeta produces `\.`. The PromQL string then needs `\\.`
// so the regex engine still sees a literal dot.
func promQLRegexLiteral(prefix string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(prefix), `\`, `\\`)
}

// applyImageAllowlist inserts extra prefixes into the shipped rule. An empty
// list returns the file unchanged. The marker is replaced once.
func applyImageAllowlist(rawYAML string, extras []string) (string, error) {
	if len(extras) == 0 {
		return rawYAML, nil
	}
	if !strings.Contains(rawYAML, imageAllowlistMarker) {
		return "", fmt.Errorf("shipped UnexpectedContainerImage rule has no allowlist insertion point")
	}
	var b strings.Builder
	for _, prefix := range extras {
		b.WriteByte('|')
		b.WriteString(promQLRegexLiteral(prefix))
	}
	return strings.Replace(rawYAML, imageAllowlistMarker, b.String()+imageAllowlistMarker, 1), nil
}
