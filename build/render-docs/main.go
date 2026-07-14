// render-docs converts the plugin's docs/*.md files into a self-contained
// HTML site under public/help/. Output is intentionally static so it can
// be served as plugin public assets (Mattermost serves anything under
// <plugin>/public/ at /plugins/<plugin-id>/public/<path>).
//
// Layout mirrors the crossguard plugin's docs site — sidebar on the left
// with topic links, main content on the right. One shared styles.css.
//
// Run via `make render-docs`. Idempotent — safe to re-run.
package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// mdLinkRe matches an HTML href whose target ends in `.md` (optionally
// followed by an anchor like #section). Captures: (1) the path before
// the extension, (2) the optional anchor including the #.
//
// Used to rewrite cross-page references in runbook source files —
// authors write [Other Runbook](other-runbook.md) for portability with
// any plain-markdown viewer, but the HTML site needs .html.
//
// External absolute URLs ending in .md (e.g., GitHub README links) are
// detected separately and skipped — they're meant to stay as .md.
var mdLinkRe = regexp.MustCompile(`href="([^"]+)\.md(#[^"]*)?"`)

// repoURL returns the GitHub URL the docs site should link to. The
// Makefile passes the resolved URL via PLUGIN_REPO_URL when invoking
// `go run`. Falls back to the upstream URL when running this binary
// directly (out of band of make), so manual `go run` still works.
func repoURL() string {
	if v := os.Getenv("PLUGIN_REPO_URL"); v != "" {
		return v
	}
	return "https://github.com/mattermost/mattermost-plugin-alertmanager"
}

// siteSection describes one rendered HTML site — a (sourceDir, outputDir,
// siteName, landingBody) tuple. We render two of these in lockstep: the
// plugin documentation under public/help/, and the SRE runbook library
// under public/runbooks/. They use the same template + styles but have
// separate navs (you don't want runbooks listed in the docs sidebar or
// vice versa).
//
// Paths are relative to build/render-docs/'s working directory, which is
// where `go run main.go` is invoked from.
type siteSection struct {
	SrcDir      string
	OutDir      string
	SiteName    string
	LandingBody string
	SkipFiles   map[string]bool // filenames to skip (e.g., INDEX.md, TEMPLATE.md)
	ExtraPages  []page          // pre-rendered pages with no .md source (e.g. the sample-rules page)
}

var sections = []siteSection{
	{
		SrcDir:      "../../docs",
		OutDir:      "../../public/help",
		SiteName:    "Mattermost Alertmanager Plugin",
		LandingBody: helpIndexBody,
	},
	{
		SrcDir:      "../../runbooks",
		OutDir:      "../../public/runbooks",
		SiteName:    "SRE Runbooks",
		LandingBody: runbookIndexBody,
		// INDEX.md is rendered as home.html separately; TEMPLATE.md is a
		// boilerplate file authors copy from, not a real runbook page.
		SkipFiles: map[string]bool{"INDEX.md": true, "TEMPLATE.md": true},
	},
}

type page struct {
	Slug      string // filename without extension, lowercased — drives the link
	Title     string // taken from the first H1 of the markdown
	HTML      template.HTML
	Active    bool   // set per-render so the nav link gets the .active class
	Source    string // original filename (e.g., MIGRATION.md) for footer reference
	Generated bool   // true when HTML is pre-built (no .md source to read)
}

// pageTemplate wraps each rendered doc in a shared shell. Inlining the
// template here keeps the build tool dependency-free aside from goldmark.
const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ .Title }} — {{ .SiteName }}</title>
    <link rel="stylesheet" href="styles.css">
</head>
<body>
<div class="layout">
    <aside class="sidebar">
        <div class="sidebar-brand">
            <h2>{{ .SiteName }}</h2>
            <span class="version">{{ if .IsRunbook }}Incident Response{{ else }}Plugin Documentation{{ end }}</span>
        </div>
        <nav>
            <span class="nav-section">Documentation</span>
            <a href="home.html"{{ if eq .Slug "home" }} class="active"{{ end }}>Overview</a>
            {{ range .Pages -}}
            <a href="{{ .Slug }}.html"{{ if .Active }} class="active"{{ end }}>{{ .Title }}</a>
            {{ end -}}
        </nav>
        <div class="sidebar-footer">
            <a href="{{ .RepoURL }}">View on GitHub</a>
        </div>
    </aside>

    <main class="content">
        <div class="breadcrumb">
            <a href="home.html">Home</a>
            {{ if ne .Slug "home" -}}
            <span class="separator">/</span>
            <span>{{ .Title }}</span>
            {{ end -}}
        </div>
        <article>{{ .HTML }}</article>
        <footer class="page-footer">
            <span>Source: <code>docs/{{ .Source }}</code></span>
        </footer>
    </main>
</div>
</body>
</html>
`

// styles is intentionally inlined so the build step produces a complete
// site without needing a separate template-asset path. Modest visual
// styling — not a marketing page, just readable.
const styles = `* { box-sizing: border-box; margin: 0; padding: 0; }
body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Helvetica Neue", Helvetica, Arial, sans-serif;
    color: #1a1a1a;
    background: #f7f7f8;
    line-height: 1.6;
}
.layout { display: grid; grid-template-columns: 260px 1fr; min-height: 100vh; }
.sidebar {
    background: #1a1a23;
    color: #d4d4d4;
    padding: 24px 0;
    position: sticky;
    top: 0;
    height: 100vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
}
.sidebar-brand { padding: 0 24px 24px; border-bottom: 1px solid #2e2e3a; margin-bottom: 16px; }
.sidebar-brand h2 { font-size: 16px; color: #fff; margin-bottom: 4px; }
.sidebar-brand .version { font-size: 12px; color: #888; }
.sidebar nav { padding: 0 12px; flex: 1; }
.sidebar .nav-section {
    display: block;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: #888;
    padding: 12px 12px 4px;
}
.sidebar nav a {
    display: block;
    padding: 8px 12px;
    color: #d4d4d4;
    text-decoration: none;
    border-radius: 4px;
    font-size: 14px;
}
.sidebar nav a:hover { background: #2a2a36; color: #fff; }
.sidebar nav a.active { background: #1d3a8c; color: #fff; }
.sidebar-footer { padding: 24px; border-top: 1px solid #2e2e3a; font-size: 12px; }
.sidebar-footer a { color: #888; text-decoration: none; }
.sidebar-footer a:hover { color: #fff; }
.content { padding: 32px 48px; max-width: 920px; }
.breadcrumb {
    font-size: 13px;
    color: #666;
    margin-bottom: 24px;
}
.breadcrumb a { color: #1d3a8c; text-decoration: none; }
.breadcrumb a:hover { text-decoration: underline; }
.breadcrumb .separator { margin: 0 8px; color: #999; }
article h1 { font-size: 28px; margin: 0 0 16px; color: #111; }
article h2 { font-size: 22px; margin: 32px 0 12px; color: #1a1a1a; border-bottom: 1px solid #e5e5e8; padding-bottom: 6px; }
article h3 { font-size: 17px; margin: 24px 0 8px; color: #1a1a1a; }
article h4 { font-size: 15px; margin: 16px 0 6px; color: #333; }
article p { margin: 0 0 12px; }
article ul, article ol { margin: 0 0 12px 24px; }
article li { margin: 4px 0; }
article code {
    background: #eef0f3;
    color: #c7254e;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: "SFMono-Regular", Menlo, Monaco, Consolas, monospace;
    font-size: 13px;
}
article pre {
    background: #1e1e2a;
    color: #e2e8f0;
    padding: 14px 18px;
    border-radius: 6px;
    overflow-x: auto;
    margin: 0 0 16px;
    line-height: 1.5;
}
article pre code { background: transparent; color: inherit; padding: 0; font-size: 13px; }
article a { color: #1d3a8c; text-decoration: none; }
article a:hover { text-decoration: underline; }
article table { border-collapse: collapse; margin: 12px 0; width: 100%; }
article th, article td {
    border: 1px solid #e5e5e8;
    padding: 8px 12px;
    text-align: left;
    font-size: 14px;
}
article th { background: #f2f3f5; font-weight: 600; }
article blockquote {
    border-left: 3px solid #1d3a8c;
    background: #eef1f8;
    padding: 8px 16px;
    margin: 12px 0;
    color: #444;
}
article hr { border: none; border-top: 1px solid #e5e5e8; margin: 24px 0; }
.page-footer {
    margin-top: 48px;
    padding-top: 16px;
    border-top: 1px solid #e5e5e8;
    font-size: 12px;
    color: #888;
}
.page-footer code { background: #eef0f3; color: #666; }
`

// helpIndexBody is the landing-page content for the docs site.
const helpIndexBody = `# Mattermost Alertmanager Plugin Documentation

This documentation ships embedded in the plugin binary and is available at
` + "`/plugins/alertmanager/public/help/`" + ` on any Mattermost server where
the plugin is installed.

## Where to go

- **[Quickstart](quickstart.html)** — fresh install to a first real alert
  landing in a channel, in eight tagged steps.
- **[Configuration](configuration.html)** — JSON schema, naming
  convention, multi-tenant patterns, validation behavior, multiple
  Alertmanagers.
- **[Slash Commands](slash_commands.html)** — full reference with worked
  examples.
- **[Migration](migration.html)** — upgrade guides between major versions,
  including the v0.4.x → v1.0 breaking changes.
- **[Development](development.html)** — local build, test, deploy
  workflow against a Mattermost dev server.

## In-chat help

Most of this content is also accessible from inside Mattermost via slash
commands:

- ` + "`/alertmanager docs`" + ` — list topics
- ` + "`/alertmanager docs <topic>`" + ` — print a specific topic

The chat output is the same source markdown that's rendered into this
site, so reading either form gets you to the same place.
`

// runbookIndexBody is the landing page for the runbook library.
const runbookIndexBody = `# SRE Runbooks

30 runbooks covering the most common alert categories an SRE
encounters. Each follows the same structure: severity → what this
means → diagnostic steps → common causes & fixes → escalation →
post-incident.

When an alert fires in Mattermost, its post includes a
[runbook_url](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/)
annotation pointing at the matching page here. Click the link in chat,
you land on the runbook, you work the alert.

## Categories

**Compute & containers** — High CPU Usage, High Memory Usage, CPU
Throttling High, Pod CrashLoopBackOff, Pod Not Ready, Image Pull
BackOff, Pods Unschedulable, Deployment Replicas Unavailable, Node Not
Ready.

**Application** — High HTTP 5xx Error Rate, High API Latency, Service
Endpoint Down, Request Rate Anomaly.

**Database** — Database Connectivity Loss, Database Replication Lag,
Database High Latency, Postgres Connections Near Max.

**Storage** — Persistent Volume Full, Disk Fill Rate High.

**Networking** — Ingress High 5xx, Certificate Expiring Soon, DNS
Resolution Failure.

**Observability** — Prometheus Scrape Target Down, Alertmanager
Notification Failure.

**Security** — Unexpected Container Image, API Server Auth Failure
Spike, Privileged Container Started, Interactive Shell in Container,
RBAC Privilege Escalation, Security Tooling Down.

Use the sidebar on the left to navigate. Use the search box up top to
jump straight to a runbook by name or by error string.

## Authoring conventions

If you're writing a new runbook, copy ` + "`runbooks/TEMPLATE.md`" + ` as
the starting point. Fill in every section — don't leave placeholder
text that might get read mid-incident. Use real ` + "`kubectl`" + `
commands with real namespaces, not ` + "`<NAMESPACE>`" + `
placeholders. The on-call wants to copy-paste.
`

// rulesSrcPath / rulesOutYAML — the shipped sample rules and where the raw
// copy lands in the help site. Relative to build/render-docs/'s working dir.
const (
	rulesSrcPath = "../../samples/prometheus-rules.yaml"
	rulesOutYAML = "../../public/help/prometheus-rules.yaml"
)

func main() {
	// Attach the generated "Sample Prometheus rules" page to the help site
	// (sections[0]). Built live from the sample yaml so the rendered page
	// can't drift from the file users actually download.
	rulesPage, err := buildRulesPage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "render-docs:", err)
		os.Exit(1)
	}
	sections[0].ExtraPages = append(sections[0].ExtraPages, rulesPage)

	totalPages := 0
	for _, s := range sections {
		n, err := renderSection(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "render-docs:", err)
			os.Exit(1)
		}
		totalPages += n
	}

	// Ship the raw yaml next to the rendered page so the download link (and
	// any curl) resolves to the real file. Done after render so the help
	// OutDir already exists.
	if err := copyRawRules(); err != nil {
		fmt.Fprintln(os.Stderr, "render-docs:", err)
		os.Exit(1)
	}

	fmt.Printf("rendered %d total pages across %d sites (+ raw prometheus-rules.yaml)\n", totalPages, len(sections))
}

// buildRulesPage renders the shipped sample Prometheus rules into a help-site
// page — the yaml verbatim (HTML-escaped) in a code block, with an intro and a
// download link. Single source of truth: the same samples/prometheus-rules.yaml
// users download, so there's no markdown copy to drift out of sync.
func buildRulesPage() (page, error) {
	raw, err := os.ReadFile(rulesSrcPath)
	if err != nil {
		return page{}, fmt.Errorf("read %s: %w", rulesSrcPath, err)
	}
	intro := `<p>Sample Prometheus alerting rules shipped with the plugin — one working rule for every runbook (31 rules across 30 runbooks). Copy them into your Prometheus, tune the thresholds, and alerts route straight to the matching runbook in chat.</p>` +
		`<p><a href="prometheus-rules.yaml" download><strong>⬇ Download prometheus-rules.yaml</strong></a> — add it to your Prometheus <code>rule_files:</code> glob, or wrap the <code>groups:</code> block in a <code>PrometheusRule</code> CRD if you run the Prometheus Operator.</p>` +
		`<blockquote>Every threshold and <code>for:</code> duration below is a demo-cluster placeholder — tune to your traffic and SLOs before trusting them in production. The <code>runbook:</code> label on each rule is the join key that routes it to a runbook.</blockquote>`
	body := intro + "<pre><code>" + template.HTMLEscapeString(string(raw)) + "</code></pre>"
	return page{
		Slug:      "prometheus-rules",
		Title:     "Sample Prometheus rules",
		HTML:      template.HTML(body),
		Source:    "samples/prometheus-rules.yaml",
		Generated: true,
	}, nil
}

// copyRawRules writes the sample rules verbatim into the help site so the
// page's download link and any `curl` resolve to the actual file.
func copyRawRules() error {
	raw, err := os.ReadFile(rulesSrcPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", rulesSrcPath, err)
	}
	if err := os.WriteFile(rulesOutYAML, raw, 0644); err != nil {
		return fmt.Errorf("write %s: %w", rulesOutYAML, err)
	}
	return nil
}

// renderSection processes one site's markdown → HTML transform. Reads
// srcDir/*.md (excluding files in section.SkipFiles), renders each, and
// writes to outDir/. Also writes a styles.css and a home.html landing
// page from section.LandingBody.
//
// Returns the count of topic pages rendered (not counting home.html).
func renderSection(section siteSection) (int, error) {
	if err := os.MkdirAll(section.OutDir, 0755); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", section.OutDir, err)
	}

	entries, err := os.ReadDir(section.SrcDir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", section.SrcDir, err)
	}

	isRunbook := strings.Contains(section.OutDir, "runbooks")

	// Discover source markdown files. SkipFiles excludes meta files like
	// INDEX.md (rendered separately as home.html) and TEMPLATE.md (an
	// author scaffold, not a real page).
	pages := make([]page, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if section.SkipFiles[e.Name()] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(section.SrcDir, e.Name()))
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		title := extractTitle(string(body), e.Name())
		slug := strings.ToLower(strings.TrimSuffix(e.Name(), ".md"))
		pages = append(pages, page{
			Slug:   slug,
			Title:  title,
			HTML:   "",
			Source: e.Name(),
		})
	}

	// Pre-rendered pages (no .md source, e.g. the sample-rules page) join
	// the nav alongside the discovered markdown pages.
	pages = append(pages, section.ExtraPages...)

	// Stable nav order — alphabetical. Predictable across builds.
	sort.Slice(pages, func(i, j int) bool { return pages[i].Slug < pages[j].Slug })

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	tmpl, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return 0, fmt.Errorf("parse template: %w", err)
	}

	for i, p := range pages {
		// Generated pages already carry their HTML — there's no markdown
		// source to read. Everything else renders from its .md file.
		if !p.Generated {
			body, err := os.ReadFile(filepath.Join(section.SrcDir, p.Source))
			if err != nil {
				return 0, fmt.Errorf("read %s: %w", p.Source, err)
			}
			var buf bytes.Buffer
			if err := md.Convert(body, &buf); err != nil {
				return 0, fmt.Errorf("render %s: %w", p.Source, err)
			}
			p.HTML = template.HTML(rewriteMDLinks(buf.String()))
		}

		if err := writePage(tmpl, section, pages, p, i, isRunbook); err != nil {
			return 0, err
		}
	}

	// Landing page (home.html) — generated from section.LandingBody.
	var indexBuf bytes.Buffer
	if err := md.Convert([]byte(section.LandingBody), &indexBuf); err != nil {
		return 0, fmt.Errorf("render landing for %s: %w", section.SiteName, err)
	}
	// Named home.html not index.html because Go's http.ServeFile
	// auto-redirects /index.html → ./, and Mattermost's plugin public
	// file handler 404s URLs ending in /.
	indexPage := page{Slug: "home", Title: "Overview", HTML: template.HTML(indexBuf.String()), Source: "(generated)"}
	if err := writePage(tmpl, section, pages, indexPage, -1, isRunbook); err != nil {
		return 0, err
	}

	if err := os.WriteFile(filepath.Join(section.OutDir, "styles.css"), []byte(styles), 0644); err != nil {
		return 0, fmt.Errorf("write styles.css: %w", err)
	}

	// Mirror any images/ subdir so `![](images/foo.png)` tags resolve in the
	// rendered site. render-docs owns the whole site, so it copies the images
	// itself rather than leaving it as a manual step authors forget.
	imgCount, err := copyImages(section.SrcDir, section.OutDir)
	if err != nil {
		return 0, fmt.Errorf("copy images: %w", err)
	}
	if imgCount > 0 {
		fmt.Printf("copied %d images to %s/images\n", imgCount, section.OutDir)
	}

	fmt.Printf("rendered %d topic pages + index to %s\n", len(pages), section.OutDir)
	return len(pages), nil
}

// copyImages mirrors an optional images/ subdir from the section's source
// dir into its output dir. Markdown image tags use relative paths like
// `images/foo.png`, which resolve to <outDir>/images/foo.png once rendered —
// Mattermost then serves them at /plugins/<id>/public/help/images/foo.png.
//
// A missing images/ dir is not an error: most doc sets have no images, and
// this keeps the tool a no-op for them. The copy is a flat, one-level mirror
// (no nested trees) because the docs convention is a single flat images/ dir.
func copyImages(srcDir, outDir string) (int, error) {
	srcImages := filepath.Join(srcDir, "images")
	entries, err := os.ReadDir(srcImages)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // no images/ subdir — nothing to mirror
		}
		return 0, fmt.Errorf("read %s: %w", srcImages, err)
	}

	outImages := filepath.Join(outDir, "images")
	if err := os.MkdirAll(outImages, 0755); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", outImages, err)
	}

	copied := 0
	for _, e := range entries {
		if e.IsDir() {
			continue // flat images/ dir only — skip any nested subdirs
		}
		data, err := os.ReadFile(filepath.Join(srcImages, e.Name()))
		if err != nil {
			return copied, fmt.Errorf("read image %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(outImages, e.Name()), data, 0644); err != nil {
			return copied, fmt.Errorf("write image %s: %w", e.Name(), err)
		}
		copied++
	}
	return copied, nil
}

// writePage renders a single page using the nav built from `pages` and
// the section metadata for site name and runbook flag.
func writePage(tmpl *template.Template, section siteSection, pages []page, p page, activeIdx int, isRunbook bool) error {
	navPages := make([]page, len(pages))
	copy(navPages, pages)
	if activeIdx >= 0 {
		navPages[activeIdx].Active = true
	}

	data := struct {
		Slug      string
		Title     string
		HTML      template.HTML
		Source    string
		Pages     []page
		SiteName  string
		IsRunbook bool
		RepoURL   string
	}{p.Slug, p.Title, p.HTML, p.Source, navPages, section.SiteName, isRunbook, repoURL()}

	out, err := os.Create(filepath.Join(section.OutDir, p.Slug+".html"))
	if err != nil {
		return fmt.Errorf("create %s.html: %w", p.Slug, err)
	}
	defer out.Close()
	if err := tmpl.Execute(out, data); err != nil {
		return fmt.Errorf("execute template for %s: %w", p.Slug, err)
	}
	return nil
}

// rewriteMDLinks turns relative .md hrefs in rendered HTML into .html
// hrefs so cross-page navigation in the rendered site works. Goldmark
// emits link destinations verbatim from the markdown source, which
// uses .md for portability with non-HTML markdown viewers.
//
// External links (anything with a `://` scheme) are left alone — those
// are real .md files on external sites and should keep their extension.
func rewriteMDLinks(html string) string {
	return mdLinkRe.ReplaceAllStringFunc(html, func(match string) string {
		sub := mdLinkRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		path := sub[1]
		anchor := ""
		if len(sub) >= 3 {
			anchor = sub[2]
		}
		// Skip external URLs — they point at real .md files (e.g.,
		// GitHub READMEs) and we don't host their .html equivalents.
		if strings.Contains(path, "://") {
			return match
		}
		return fmt.Sprintf(`href="%s.html%s"`, path, anchor)
	})
}

// extractTitle pulls the first H1 from a markdown file as the page title.
// Falls back to a humanized filename if no H1 is present — defensive
// against docs that start with a frontmatter block or skip H1.
func extractTitle(body, filename string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	// Fallback: humanize the filename.
	name := strings.TrimSuffix(filename, ".md")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.Title(strings.ToLower(name))
}
