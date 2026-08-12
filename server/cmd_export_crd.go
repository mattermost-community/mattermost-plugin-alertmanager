package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// CRD export path (`/alertmanager export --format=crd` and `add --format=crd`).
// Groups channel-scoped receivers by their shared webhook (the plugin's
// one-webhook-per-group model), and emits, per group, a Secret + an
// AlertmanagerConfig (v1alpha1). See docs/KUBERNETES.md.

// defaultCRDNamespace is where the generated Secret + AlertmanagerConfig land
// unless overridden with --namespace=. kube-prometheus-stack's default.
const defaultCRDNamespace = "monitoring"

// Output formats accepted by the `--format=` flag on add/export.
const (
	formatStandard = "standard" // flat alertmanager.yml
	formatCRD      = "crd"      // Prometheus Operator AlertmanagerConfig
)

// crdNamespaceRegex matches a valid Kubernetes namespace name: an RFC 1123 DNS
// label — lowercase alphanumerics and '-', starting and ending alphanumeric.
var crdNamespaceRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// validateCRDNamespace rejects a user-supplied --namespace that isn't a valid
// Kubernetes namespace. The value comes straight from the slash-command args
// and flows into generated AlertmanagerConfig/Secret manifests
// (metadata.namespace), so an unvalidated value could yield broken or injected
// YAML. We reject rather than silently coerce (unlike sanitizeK8sName) so the
// operator isn't surprised by output landing in a different namespace.
func validateCRDNamespace(ns string) error {
	if len(ns) > 63 {
		return fmt.Errorf("namespace %q is too long (max 63 characters)", ns)
	}
	if !crdNamespaceRegex.MatchString(ns) {
		return fmt.Errorf("namespace %q is not a valid Kubernetes namespace (lowercase letters, digits and '-'; must start and end with a letter or digit)", ns)
	}
	return nil
}

// sanitizeK8sName lowercases and coerces s into an RFC1123-ish DNS label
// (a-z, 0-9, '-'), collapsing any other run to a single '-' and trimming
// leading/trailing '-'. Mattermost channel/team slugs are already close, so
// this is usually a no-op; it defends against the occasional stray character.
func sanitizeK8sName(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// assembleCRDManifest builds the full multi-document manifest (Secret +
// AlertmanagerConfig per shared-webhook group) for the given receivers, joined
// with `---`. Returns the manifest and the number of AlertmanagerConfigs.
func (p *Plugin) assembleCRDManifest(scoped []alertConfig, namespace string) (string, int) {
	// Preserve first-seen order so output is deterministic across runs.
	type group struct {
		url     string
		channel string
		specs   []crdReceiverSpec
	}
	order := make([]string, 0)
	groups := make(map[string]*group)

	for _, ac := range scoped {
		url := p.webhookURLForReceiver(ac)
		g := groups[url]
		if g == nil {
			g = &group{url: url, channel: ac.Channel}
			groups[url] = g
			order = append(order, url)
		}
		slug := receiverBaseSlug(ac.Name)
		g.specs = append(g.specs, crdReceiverSpec{
			slug:              slug,
			name:              ac.Name,
			channel:           ac.Channel,
			runbookDefaultURL: p.runbookDefaultURL(slug),
			iconURL:           p.siteURL() + webhookIconURL,
			custom:            ac.Custom,
		})
	}

	var out strings.Builder
	for i, url := range order {
		g := groups[url]
		// Suffix disambiguates when one channel has receivers across multiple
		// shared webhooks (multiple /alertmanager add groups).
		suffix := ""
		if len(order) > 1 {
			suffix = fmt.Sprintf("-%d", i+1)
		}
		chanName := sanitizeK8sName(g.channel)
		secretName := "alertmanager-webhook-" + chanName + suffix
		fallbackName := chanName + suffix + "-fallback"
		crName := "mattermost-alertmanager-" + chanName + suffix

		// Prepend the synthesized fallback receiver (the parent route's
		// catch-all), then the group's receivers — all sharing one Secret.
		specs := make([]crdReceiverSpec, 0, len(g.specs)+1)
		specs = append(specs, crdReceiverSpec{
			name:       fallbackName,
			secretName: secretName,
			channel:    g.channel,
			iconURL:    p.siteURL() + webhookIconURL,
		})
		for _, s := range g.specs {
			s.secretName = secretName
			specs = append(specs, s)
		}

		if i > 0 {
			out.WriteString("---\n")
		}
		out.WriteString(renderWebhookSecret(secretName, namespace, g.url))
		out.WriteString("---\n")
		out.WriteString(renderAlertmanagerConfig(crName, namespace, fallbackName, specs))
		out.WriteString("\n")
	}
	return out.String(), len(order)
}

// handleExportCRD is the `export --format=crd` path: assemble the
// AlertmanagerConfig + Secret manifest for the channel's receivers and DM it.
func (p *Plugin) handleExportCRD(args *model.CommandArgs, scoped []alertConfig, namespace string) (string, error) {
	manifest, crCount := p.assembleCRDManifest(scoped, namespace)

	if err := p.dmCRDBundle(args.UserId, manifest, crCount, namespace); err != nil {
		// DM failed — inline the manifest so the operator config isn't lost.
		return fmt.Sprintf(
			":warning: Couldn't DM the manifest (%v). Inline copy below — review and `kubectl apply -n %s`:\n\n```yaml\n%s```\n",
			err, namespace, manifest,
		), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":page_facing_up: Exported %d receiver(s) as **%d AlertmanagerConfig(s)** (v1alpha1) to your DM with `@%s`.\n\n", len(scoped), crCount, webhookUsername))
	b.WriteString(fmt.Sprintf("Open the DM, review `alertmanager-config.yaml`, then `kubectl apply -n %s -f alertmanager-config.yaml`. The Secret carries the webhook URL and the file is auto-deleted after the configured TTL.\n\n", namespace))
	b.WriteString(":book: Details + operator gotchas: `/alertmanager docs kubernetes`.\n")
	return b.String(), nil
}

// dmCRDBundle DMs the assembled CRD manifest as a single file, with kubectl
// apply guidance. Mirrors dmYAMLBundle's delivery + auto-delete tracking (the
// manifest embeds webhook URLs in the Secret, so it's TTL-cleaned too).
func (p *Plugin) dmCRDBundle(userID, manifest string, crCount int, namespace string) error {
	dm, appErr := p.API.GetDirectChannel(p.BotUserID, userID)
	if appErr != nil {
		return fmt.Errorf("open DM with user: %w", appErr)
	}

	info, appErr := p.API.UploadFile([]byte(manifest), dm.Id, "alertmanager-config.yaml")
	if appErr != nil {
		return fmt.Errorf("upload CRD manifest to DM: %w", appErr)
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Assembled **%d AlertmanagerConfig(s)** + their Secret(s) for this channel (Prometheus Operator, v1alpha1).\n\n", crCount))
	msg.WriteString(fmt.Sprintf("Review `alertmanager-config.yaml`, then apply into the `%s` namespace:\n\n", namespace))
	msg.WriteString(fmt.Sprintf("```\nkubectl apply -n %s -f alertmanager-config.yaml\n```\n", namespace))
	msg.WriteString("\nThe Secret carries the webhook URL — this file is auto-deleted after the configured TTL. See `/alertmanager docs kubernetes`.")

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: dm.Id,
		Message:   msg.String(),
		FileIds:   []string{info.Id},
	}
	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		return fmt.Errorf("post to DM: %w", appErr)
	}
	p.trackYAMLForAutoDelete(created.Id)
	return nil
}
