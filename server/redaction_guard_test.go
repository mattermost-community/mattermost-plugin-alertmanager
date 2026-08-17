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
// never reach a log line or an error string: webhook IDs (durable bearer tokens
// for /hooks/<id> — CL-13) and Alertmanager basic-auth passwords.
var sensitiveArgRegex = regexp.MustCompile(`(?i)^(.*hook_?id|password|passwd)$`)

// TestNoRawSecretInLogsOrErrors is a STRUCTURAL guard against the recurring
// failure mode this codebase kept hitting in review: a secret redacted at one
// sink but passed raw to its sibling (a log field redacted while the same line's
// error string carried the raw ID; a hook ID redacted in five places but not the
// sixth). Rather than rely on a reviewer noticing every site, this walks the
// package AST and fails if a bare webhook-ID / password identifier is passed
// DIRECTLY to a Log* call or fmt.Errorf.
//
// The check deliberately does NOT descend into call expressions: redactHookID(x)
// and err.Error() are sanitized boundaries (the redaction, or the error's own
// construction which this test also checks). So the only accepted way to put a
// hook ID in a log/error is redactHookID(hookID). Chat-post fmt.Sprintf (the
// accepted B-003 raw-ID-in-channel display for remediation) is a different
// function and is intentionally not checked.
func TestNoRawSecretInLogsOrErrors(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	var violations []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isLogOrErrorfCall(call.Fun) {
				return true
			}
			for _, arg := range call.Args {
				if name := findSensitiveIdent(arg); name != "" {
					pos := fset.Position(arg.Pos())
					violations = append(violations, fmt.Sprintf(
						"%s:%d: %q reaches a log/error unredacted — wrap it (e.g. redactHookID(%s))",
						pos.Filename, pos.Line, name, name))
				}
			}
			return true
		})
	}

	if len(violations) > 0 {
		t.Fatalf("secret values reach logs/errors unredacted (CL-13); redact them at the sink:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// isLogOrErrorfCall reports whether fun is a p.API.Log* call or fmt.Errorf — the
// two sinks whose output reaches the log pipeline (Log* directly; an error via
// err.Error() at the call sites that log it).
func isLogOrErrorfCall(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "LogWarn", "LogError", "LogInfo", "LogDebug":
		return true
	case "Errorf":
		x, ok := sel.X.(*ast.Ident)
		return ok && x.Name == "fmt"
	}
	return false
}

// findSensitiveIdent returns the first sensitive identifier reachable from arg
// WITHOUT crossing a call expression, or "" if none. Not descending into calls is
// what makes redactHookID(hookID) and err.Error() accepted while a bare hookID or
// ac.WebhookID (or a "x"+hookID concat) is caught.
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
