package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// runbookSlugsFromFS returns the slug of every embedded runbook, excluding
// the non-runbook TEMPLATE.md and INDEX.md.
func runbookSlugsFromFS(t *testing.T) []string {
	t.Helper()
	paths, err := fs.Glob(root.RunbooksFS, "runbooks/*.md")
	if err != nil {
		t.Fatalf("glob embedded runbooks: %v", err)
	}
	var slugs []string
	for _, p := range paths {
		base := strings.TrimSuffix(strings.TrimPrefix(p, "runbooks/"), ".md")
		if base == "TEMPLATE" || base == "INDEX" {
			continue
		}
		slugs = append(slugs, base)
	}
	if len(slugs) == 0 {
		t.Fatal("no embedded runbooks found — embed pattern broken?")
	}
	return slugs
}

// scaffoldSlugSet is the union of every category set's slugs ("all" excluded —
// it's resolved at runtime from the embedded FS, not enumerated here).
func scaffoldSlugSet() map[string]bool {
	set := map[string]bool{}
	for name, list := range scaffoldSets {
		if name == "all" {
			continue
		}
		for _, s := range list {
			set[s] = true
		}
	}
	return set
}

// TestScaffoldMatchesRunbookFiles is the drift guard: the set of runbooks the
// scaffold offers must equal the set of runbooks that actually ship. Catches
// both directions — a scaffoldSets entry with no runbook file (add would fail
// / link 404s), and a shipped runbook missing from every category (only
// reachable via `all`, invisible to per-category add/remove/validate).
func TestScaffoldMatchesRunbookFiles(t *testing.T) {
	files := map[string]bool{}
	for _, s := range runbookSlugsFromFS(t) {
		files[s] = true
	}
	scaffold := scaffoldSlugSet()

	for slug := range scaffold {
		if !files[slug] {
			t.Errorf("scaffoldSets references %q but runbooks/%s.md does not exist", slug, slug)
		}
	}
	for slug := range files {
		if !scaffold[slug] {
			t.Errorf("runbook %q ships but is not in any scaffoldSets category (unreachable via per-category commands)", slug)
		}
	}
}

// knownLiteralPlaceholders are `<tag>`-shaped strings that legitimately appear
// as literal command text, not as label placeholders. kubectl prints "<none>"
// for empty fields, so a runbook may grep for it. Anything matching the
// placeholder regex that is neither an allowlisted label nor a known literal is
// flagged — it's almost certainly a typo'd label (e.g. <podname> for <pod>)
// that would render literally instead of substituting. A genuinely new literal
// gets added here as a reviewed, one-line decision.
var knownLiteralPlaceholders = map[string]bool{
	"none": true, // kubectl's empty-field marker
}

// TestRunbookPlaceholdersAllowlisted scans the Quick Diagnostics of every
// runbook for `<label>` placeholders on executable (non-comment) lines and
// fails if any is neither an allowlisted label nor a known literal. An
// un-allowlisted tag like <podname> isn't substituted — it renders as the
// literal text "<podname>" in the alert post instead of the value. This is the
// only check that catches that silent typo.
func TestRunbookPlaceholdersAllowlisted(t *testing.T) {
	for _, slug := range runbookSlugsFromFS(t) {
		blocks := loadQuickDiagnosticsForSlug(slug)
		for _, blk := range blocks {
			for line := range strings.SplitSeq(blk.Code, "\n") {
				// Mirror substituteLabelPlaceholders: comment lines are
				// skipped (they reference labels in prose, not commands).
				trim := strings.TrimLeft(line, " \t")
				if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "--") {
					continue
				}
				for _, m := range labelPlaceholderRegex.FindAllStringSubmatch(line, -1) {
					name := m[1]
					if !labelPlaceholderAllowlist[name] && !knownLiteralPlaceholders[name] {
						t.Errorf("runbook %q: placeholder <%s> is neither an allowlisted label nor a known literal — it will render literally, not substituted (line: %q). If it's a real label, add it to labelPlaceholderAllowlist; if intentional literal text, add it to knownLiteralPlaceholders.", slug, name, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

// TestDocTopicsResolve asserts every registered `/alertmanager docs <topic>`
// maps to a real embedded doc file. Catches a topic pointing at a renamed or
// missing file (which would 500 the command at runtime).
func TestDocTopicsResolve(t *testing.T) {
	for topic, filename := range docTopics {
		if _, err := root.DocsFS.ReadFile("docs/" + filename); err != nil {
			t.Errorf("docs topic %q -> %q is not an embedded doc: %v", topic, filename, err)
		}
	}
}

// collectStaticListItems walks an AutocompleteData tree and returns every
// static-list item value it finds (across all subcommands and args).
func collectStaticListItems(data *model.AutocompleteData) []string {
	var items []string
	for _, arg := range data.Arguments {
		if sl, ok := arg.Data.(*model.AutocompleteStaticListArg); ok {
			for _, it := range sl.PossibleArguments {
				items = append(items, it.Item)
			}
		}
	}
	for _, sub := range data.SubCommands {
		items = append(items, collectStaticListItems(sub)...)
	}
	return items
}

// TestScaffoldCategoriesDocumented guards against the count/label drift that
// staled the docs repeatedly: every scaffoldSets category must be surfaced in
// both the help text and the command autocomplete, so adding a category can't
// silently leave it undiscoverable.
func TestScaffoldCategoriesDocumented(t *testing.T) {
	autocompleteItems := map[string]bool{}
	for _, it := range collectStaticListItems(getAutocompleteData()) {
		autocompleteItems[it] = true
	}

	for name := range scaffoldSets {
		if name == "all" {
			continue
		}
		if !strings.Contains(helpMsg, "`"+name+"`") {
			t.Errorf("scaffold category %q is not mentioned in helpMsg", name)
		}
		if !autocompleteItems[name] {
			t.Errorf("scaffold category %q is not offered in any command autocomplete static list", name)
		}
	}
}
