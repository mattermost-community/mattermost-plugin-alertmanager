package main

import (
	_ "embed"
	"html/template"
)

//go:embed inventory.html
var inventoryHTML string

// inventoryTemplate is the HTML shell for /admin/inventory: reconciler health
// banner, per-team rollup, group-by toggle, CSV export, client-side search,
// per-AM reachability dots and clickable channel names.
var inventoryTemplate = template.Must(template.New("inventory").Parse(inventoryHTML))
