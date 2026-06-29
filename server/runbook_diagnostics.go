package main

import (
	"regexp"
	"strings"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// labelPlaceholderAllowlist is the set of `<name>` tags a runbook
// author can put in a Quick diagnostics code block. Each entry maps
// to a Prometheus label name that AM substitutes per alert at render
// time, so the diagnostic command lands in chat with the failing
// host/pod/namespace already filled in.
//
// Only credential-free, identity-style labels are on this list. We
// don't proxy `password`, `token`, or anything else that smells like
// a secret — keeping the surface explicit prevents the "let's just
// allow .Labels.* and trust the rule author" mistake.
var labelPlaceholderAllowlist = map[string]bool{
	"alertname":  true,
	"app":        true,
	"cluster":    true,
	"container":  true,
	"deployment": true,
	"instance":   true,
	"job":        true,
	"namespace":  true,
	"node":       true,
	"pod":        true,
	"service":    true,
}

// labelPlaceholderRegex matches `<name>` where name is a lowercase
// identifier — narrow enough that it doesn't hit shell command
// substitution (`$VAR`), kubectl jsonpath (`{.status.X}`), regex
// quantifiers (`{2}`), HTML-looking content with mixed case, or Go
// template syntax (`{{ }}`).
var labelPlaceholderRegex = regexp.MustCompile(`<([a-z][a-z_]*)>`)

// substituteLabelPlaceholders rewrites `<labelname>` placeholders in
// a Quick diagnostics code block into AM Go-template directives
// `{{ .Labels.labelname }}`. Unknown placeholders pass through
// unchanged — angle brackets in shell or text content are common
// enough that "leave unfamiliar tags alone" is the right default.
//
// Comment lines are skipped entirely. Comments often reference
// placeholders by name in the surrounding explanatory text
// (e.g., "# WHERE: <namespace> is filled in by AM"), and if those
// were substituted too the rendered chat post would read like
// "# WHERE: monitoring is filled in by AM" — nonsensical. Only
// executable lines get the substitution.
//
// Comment detection is language-agnostic: lines whose first
// non-whitespace character is `#` (shell/PromQL convention) or
// whose first two non-whitespace characters are `--` (SQL
// convention). Covers every language hint we emit (bash, promql,
// sql) and anything else likely to land in a runbook.
//
// Called at YAML-render time (renderReceiverYAML) so the slack_configs
// `text:` block contains live AM template syntax; AM then evaluates
// the directives per alert at delivery time and emits the actual
// label value in the chat post.
func substituteLabelPlaceholders(code string) string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		trim := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "--") {
			continue
		}
		lines[i] = labelPlaceholderRegex.ReplaceAllStringFunc(line, func(match string) string {
			name := match[1 : len(match)-1]
			if labelPlaceholderAllowlist[name] {
				return "{{ .Labels." + name + " }}"
			}
			return match
		})
	}
	return strings.Join(lines, "\n")
}

// quickDiagnosticLimit caps how many fenced code blocks the plugin
// pulls out of a runbook's "## Quick diagnostics" section. The contract
// with runbook authors (documented in CONTRIBUTING.md and TEMPLATE.md)
// is "the first three are surfaced inline in chat" — bumping this
// changes the visual weight of every alert post and should be
// considered a UX decision, not a code change.
const quickDiagnosticLimit = 3

// quickDiagnostic captures one extracted code block from a runbook's
// Quick diagnostics section. Lang is the language hint after the
// opening fence (empty if absent); Code is the raw content between
// the fences with no transformation applied.
type quickDiagnostic struct {
	Lang string
	Code string
}

// extractQuickDiagnostics returns the first `quickDiagnosticLimit`
// fenced code blocks found under the "## Quick diagnostics" heading
// in the given markdown body. Returns an empty slice when the section
// is missing — callers must treat that as "no inline diagnostics for
// this runbook" and fall back to the URL-only render.
//
// The parser is line-based rather than AST-based because we don't need
// goldmark in the server package: the document grammar this code
// inspects is exactly two markdown features (H2 heading text + fenced
// code blocks), both of which are unambiguous at the line level. A
// line-based scanner is also resilient to runbook content using
// indented code blocks, blockquotes, or other markdown that an AST
// walker would have to handle case-by-case.
//
// Termination rules:
//   - The section ends when the next `## ` heading appears
//   - Collection stops once `limit` blocks have been captured
//   - Trailing whitespace inside a code block is preserved (it's
//     significant for `bash` heredocs and `promql` expressions)
func extractQuickDiagnostics(md []byte) []quickDiagnostic {
	var (
		out       []quickDiagnostic
		current   quickDiagnostic
		inSection bool
		inBlock   bool
		body      strings.Builder
	)

	for line := range strings.SplitSeq(string(md), "\n") {
		trim := strings.TrimSpace(line)

		// Pre-section: scan until we hit the Quick diagnostics heading.
		// Heading text is matched exactly (case-sensitive) — authors who
		// rename the section break the contract.
		if !inSection {
			if trim == "## Quick diagnostics" {
				inSection = true
			}
			continue
		}

		// In-section, not in a code block: watch for the next H2
		// heading (which terminates the section) or a fence opener
		// (which starts a code block).
		if !inBlock {
			if strings.HasPrefix(trim, "## ") {
				return out
			}
			if after, ok := strings.CutPrefix(trim, "```"); ok {
				current = quickDiagnostic{
					Lang: after,
				}
				body.Reset()
				inBlock = true
			}
			continue
		}

		// In a code block: ``` on its own closes it. Accumulate
		// everything else verbatim (including leading whitespace,
		// which can be significant for SQL or PromQL formatting).
		if trim == "```" {
			current.Code = strings.TrimRight(body.String(), "\n")
			out = append(out, current)
			body.Reset()
			inBlock = false
			if len(out) >= quickDiagnosticLimit {
				return out
			}
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	return out
}

// loadQuickDiagnosticsForSlug fetches the runbook MD file for the
// given slug from the embedded FS and extracts its Quick diagnostics
// blocks. Returns nil (not an error) when the slug has no runbook
// or the runbook has no diagnostics section — both are
// non-pathological cases the caller handles by falling back to a
// URL-only alert render.
func loadQuickDiagnosticsForSlug(slug string) []quickDiagnostic {
	if slug == "" {
		return nil
	}
	data, err := root.RunbooksFS.ReadFile("runbooks/" + slug + ".md")
	if err != nil {
		return nil
	}
	return extractQuickDiagnostics(data)
}

// formatQuickDiagnosticsForAlert renders the extracted blocks into the
// markdown chunk that gets baked into the slack_configs `text:` block
// at YAML-render time. Output is a multi-line markdown string ready
// to be inlined into AM's Go-templated message body.
//
// Returns an empty string when there are no blocks — the caller
// substitutes that into a template position where empty means
// "render nothing extra" (no leading **Quick diagnostics:** header,
// no leading whitespace).
//
// Each block becomes one numbered list item followed by a fenced code
// block with the same language hint as the source. Numbering rather
// than bullets so operators can reference "step 2" verbally during
// an incident call.
func formatQuickDiagnosticsForAlert(blocks []quickDiagnostic) string {
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Quick diagnostics:**\n")
	for i, blk := range blocks {
		// Blank line before each item keeps Mattermost's markdown
		// renderer from collapsing the numbered list into a single
		// paragraph with the preceding header.
		b.WriteString("\n")
		b.WriteString(itoaSmall(i+1) + ".\n")
		b.WriteString("```")
		b.WriteString(blk.Lang)
		b.WriteString("\n")
		b.WriteString(substituteLabelPlaceholders(blk.Code))
		b.WriteString("\n```\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// itoaSmall is a no-allocation int-to-string for the single-digit
// case the diagnostic limit guarantees. Hand-rolled because pulling
// in strconv for "convert 1, 2, or 3 to a string" is excessive.
func itoaSmall(n int) string {
	if n >= 0 && n <= 9 {
		return string(rune('0' + n))
	}
	// Fallthrough for the unexpected case — limit could one day be
	// raised past 9, and we'd rather emit a sane number than panic.
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}
