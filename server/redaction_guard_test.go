package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sensitiveArgRegex matches identifier/field names that hold a secret which must
// never reach a log line or an error string: webhook IDs and webhook URLs (both
// carry the durable /hooks/<id> bearer token — CL-13) and Alertmanager basic-auth
// passwords. Anchored full-match so it targets the actual secret-holding names.
var sensitiveArgRegex = regexp.MustCompile(`(?i)^(.*hook_?id|webhook_?url|password|passwd)$`)

// TestNoRawSecretInLogsOrErrors is a STRUCTURAL CI guard against the recurring
// failure mode this codebase kept hitting in review: a secret redacted at one
// sink but passed raw to its sibling. It walks the server package (and the
// alertmanager subpackage, where the AM passwords live) and fails if a bare
// webhook-ID / webhook-URL / password identifier is passed DIRECTLY to a Log*
// call, p.auditLog, or fmt.Errorf.
//
// It is a name-based heuristic, NOT a taint analysis — deliberately, so it stays
// cheap. Known limits (rely on review for these): it does not see a secret
// renamed to an innocuous local (`id := ac.WebhookID; log(id)`), a whole struct
// logged via %+v, or a secret laundered through a non-redact function call. What
// it DOES enforce: the only accepted way to put a hook ID in a log/error is
// redactHookID(hookID) — a CallExpr, which the walker treats as a sanitized
// boundary (it does not descend into calls). Chat-post fmt.Sprintf (the accepted
// B-003 raw-ID-in-channel display for admin remediation) is a different function
// and is intentionally not a checked sink.
func TestNoRawSecretInLogsOrErrors(t *testing.T) {
	var paths []string
	for _, pat := range []string{"*.go", "alertmanager/*.go"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %q: %v", pat, err)
		}
		paths = append(paths, m...)
	}

	fset := token.NewFileSet()
	var violations []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		violations = append(violations, scanForRawSecrets(fset, f)...)
	}

	if len(violations) > 0 {
		t.Fatalf("secret values reach logs/errors unredacted (CL-13); redact them at the sink:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestRedactionGuardDetectsKnownLeak is the negative control: it proves the
// detector actually FIRES. Without it, a refactor that broke scanForRawSecrets
// would leave TestNoRawSecretInLogsOrErrors green and useless (a clean tree
// passes a broken detector too). Each "leak" snippet must be flagged; each
// "clean" snippet must not.
func TestRedactionGuardDetectsKnownLeak(t *testing.T) {
	leaks := []string{
		`package p
import "fmt"
func f(hookID string) error { return fmt.Errorf("boom %s", hookID) }`,
		`package p
func f(api API, ac cfg) { api.LogWarn("cfg", "webhook", ac.WebhookID) }`,
		`package p
func f(api API, webhookURL string) { api.LogError("posting", "url", webhookURL) }`,
		`package p
func f(api API, password string) { api.LogInfo("auth", "password", password) }`,
		`package p
func f(p P, oldHookID string) { p.auditLog("rotate", oldHookID) }`,
	}
	for i, src := range leaks {
		if v := scanSource(t, fmt.Sprintf("leak%d.go", i), src); len(v) == 0 {
			t.Errorf("leak snippet %d was NOT flagged (detector is broken):\n%s", i, src)
		}
	}

	clean := []string{
		// redactHookID wrapping is the accepted form (a call — walker stops there).
		`package p
func f(api API, hookID string) { api.LogWarn("x", "webhook", redactHookID(hookID)) }`,
		// AlertManagerURL is loggable (not a secret) — must not false-positive.
		`package p
func f(api API, alertManagerURL string) { api.LogInfo("probe", "am", alertManagerURL) }`,
		// A non-sink fmt.Sprintf (chat message) is not checked — accepted B-003 shape.
		`package p
import "fmt"
func f(hookID string) string { return fmt.Sprintf("delete webhook %s in System Console", hookID) }`,
	}
	for i, src := range clean {
		if v := scanSource(t, fmt.Sprintf("clean%d.go", i), src); len(v) != 0 {
			t.Errorf("clean snippet %d was wrongly flagged: %v\n%s", i, v, src)
		}
	}
}

// scanSource parses a source snippet and runs the detector over it.
func scanSource(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return scanForRawSecrets(fset, f)
}

// scanForRawSecrets walks node and returns one message per sensitive identifier
// passed directly to a recognized log/error sink.
func scanForRawSecrets(fset *token.FileSet, node ast.Node) []string {
	var out []string
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSecretSink(call.Fun) {
			return true
		}
		for _, arg := range call.Args {
			if name := findSensitiveIdent(arg); name != "" {
				pos := fset.Position(arg.Pos())
				out = append(out, fmt.Sprintf(
					"%s:%d: %q reaches a log/error unredacted — wrap it (e.g. redactHookID(%s))",
					pos.Filename, pos.Line, name, name))
			}
		}
		return true
	})
	return out
}

// isSecretSink reports whether fun is a sink whose output reaches the log
// pipeline: p.API.Log*, the p.auditLog wrapper, or fmt.Errorf (an error reaches
// the logs via err.Error() at the sites that log it).
func isSecretSink(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "LogWarn", "LogError", "LogInfo", "LogDebug", "auditLog":
		return true
	case "Errorf":
		x, ok := sel.X.(*ast.Ident)
		return ok && x.Name == "fmt"
	}
	return false
}

// findSensitiveIdent returns the first sensitive identifier reachable from arg
// WITHOUT crossing a call expression, or "" if none. Not descending into calls is
// what makes redactHookID(hookID) and err.Error() accepted while a bare hookID /
// ac.WebhookID / "x"+hookID is caught.
func findSensitiveIdent(arg ast.Expr) string {
	var found string
	ast.Inspect(arg, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		if _, isCall := n.(*ast.CallExpr); isCall {
			return false // sanitized boundary — stop here
		}
		if id, ok := n.(*ast.Ident); ok && sensitiveArgRegex.MatchString(id.Name) {
			found = id.Name
			return false
		}
		return true
	})
	return found
}
