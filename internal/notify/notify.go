// Package notify pushes release events to chat channels (Lark / Teams group
// bots, or a generic JSON webhook) according to per-environment rules.
// Groups only, no @mentions, so no identity mapping is needed.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"tide/internal/i18n"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/upstream"
)

// Events a rule can subscribe to.
const (
	EventStarted   = "release.started"
	EventSucceeded = "release.succeeded"
	EventFailed    = "release.failed"
	EventCancelled = "release.cancelled"
	// EventApproval: confirmed and waiting for approvers; EventRejected: an approver rejected it.
	EventApproval = "release.approval_requested"
	EventRejected = "release.rejected"
	// EventPending: CI created a release in an environment that holds it for
	// a person. Nobody is watching Tide for it, so the channel is how they
	// find out there is something to confirm.
	EventPending = "release.pending"
	// EventBuildFailed and EventBuildWarning never become releases: the
	// first built nothing, the second built something but could not hand it
	// over. Both are green pipelines as far as GitLab is concerned, so a
	// channel is the only place they can surface.
	EventBuildFailed  = "build.failed"
	EventBuildWarning = "build.warning"
)

var Events = []string{EventPending, EventApproval, EventStarted, EventSucceeded, EventFailed, EventRejected, EventCancelled,
	EventBuildFailed, EventBuildWarning}

// A notification goes to a shared channel rather than to one person's session,
// so there is no request to take a language from. Like audit records, these
// render in the deployment's default language and read the same for everyone.
var eventText = map[string]i18n.Key{EventPending: "n.eventPending", EventApproval: "n.eventApproval", EventStarted: "n.eventStarted",
	EventSucceeded: "n.eventSucceeded", EventFailed: "n.eventFailed", EventRejected: "n.eventRejected", EventCancelled: "n.eventCancelled",
	EventBuildFailed: "n.eventBuildFailed", EventBuildWarning: "n.eventBuildWarning"}

// t renders a notification message in the deployment's language.
func t(k i18n.Key, args ...any) string { return i18n.T(i18n.Default, k, args...) }

type Notifier struct {
	Settings *settings.Store
	HTTP     *http.Client
}

func (n *Notifier) client() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ReleaseEvent takes the executor's short event names (started, succeeded,
// failed, cancelled) and delivers to every channel of every matching rule.
func (n *Notifier) ReleaseEvent(ctx context.Context, r *release.Release, event string) {
	event = "release." + event
	cfg, err := n.Settings.Notify(ctx)
	if err != nil || len(cfg.Rules) == 0 {
		return
	}
	envs, _ := n.Settings.Environments(ctx)
	tier := ""
	if e, ok := envs.Named(r.Env); ok {
		tier = e.Tier
	}
	if Suppressed(r, event) {
		return
	}
	targets := Targets(cfg, r.Env, tier, event, serviceOf(r))
	if len(targets) == 0 {
		return
	}
	sys, _ := n.Settings.System(ctx)
	for _, ch := range targets {
		if err := n.send(ctx, ch, Message{Release: r, Event: event, System: sys}); err != nil {
			zap.L().Warn("notification failed", zap.String("channel", ch.Name), zap.String("release", r.ID), zap.Error(err))
		}
	}
}

// Suppressed keeps a release to two messages: one when it is submitted
// (waiting for approval, or starting), one when it ends. A release that went
// through approval already announced itself, so its start is not repeated.
func Suppressed(r *release.Release, event string) bool {
	return event == EventStarted && r.ApprovalRule != nil
}

// Targets resolves the enabled channels for an event in env, each once.
// service narrows it further where a rule asks for that; an empty service
// only matches rules that did not.
func Targets(cfg settings.Notify, env, tier, event, service string) []settings.Channel {
	var names []string
	for _, rule := range cfg.Rules {
		if rule.Enabled && slices.Contains(rule.Events, event) && rbac.EnvMatches(rule.Envs, env, tier) &&
			ServiceMatches(rule.Services, service) {
			for _, c := range rule.Channels {
				if !slices.Contains(names, c) {
					names = append(names, c)
				}
			}
		}
	}
	var out []settings.Channel
	for _, ch := range cfg.Channels {
		if ch.Enabled && slices.Contains(names, ch.Name) {
			out = append(out, ch)
		}
	}
	return out
}

// ServiceMatches reports whether service is one a rule with these selectors
// wants. No selectors means every service: rules written before the field
// existed must keep behaving as they did.
func ServiceMatches(selectors []string, service string) bool {
	if len(selectors) == 0 {
		return true
	}
	for _, sel := range selectors {
		if sel == "*" {
			return true
		}
		if ok, err := path.Match(sel, service); err == nil && ok {
			return true
		}
	}
	return false
}

// serviceOf is the one service a release is about, or "" when it touches
// several — a rule scoped to one service should not fire on a batch that
// merely happens to include it.
func serviceOf(r *release.Release) string {
	name := ""
	for _, it := range r.Items {
		s := ""
		if p, err := r.RestartPayload(it); err == nil {
			s = p.Service
		} else if p, err := r.ImagePayload(it); err == nil {
			s = p.Service
		}
		if s == "" {
			continue
		}
		if name != "" && name != s {
			return ""
		}
		name = s
	}
	return name
}

// BuildEvent delivers a pipeline outcome that will never become a release.
// Unlike ReleaseEvent there is nothing to suppress and nothing to look up:
// the pipeline told Tide everything this message contains.
func (n *Notifier) BuildEvent(ctx context.Context, b Build, event string) {
	cfg, err := n.Settings.Notify(ctx)
	if err != nil || len(cfg.Rules) == 0 {
		return
	}
	envs, _ := n.Settings.Environments(ctx)
	tier := ""
	if e, ok := envs.Named(b.Env); ok {
		tier = e.Tier
	}
	targets := Targets(cfg, b.Env, tier, event, b.Service)
	if len(targets) == 0 {
		return
	}
	sys, _ := n.Settings.System(ctx)
	for _, ch := range targets {
		if err := n.send(ctx, ch, Message{Build: &b, Event: event, System: sys}); err != nil {
			zap.L().Warn("notification failed", zap.String("channel", ch.Name),
				zap.String("service", b.Service), zap.Error(err))
		}
	}
}

// Test sends a test message through ch regardless of rules or enabled state.
func (n *Notifier) Test(ctx context.Context, ch settings.Channel) error {
	sys, _ := n.Settings.System(ctx)
	return n.send(ctx, ch, Message{Text: t("n.test", sys.SiteName, ch.Name)})
}

// Message is one notification: either a release event (rendered per channel
// kind) or plain text.
type Message struct {
	Release *release.Release
	Build   *Build
	Event   string
	System  settings.System
	Text    string
}

// Build is a pipeline outcome that produced no release: a failed build, or
// one that succeeded and could not tell Tide about it.
type Build struct {
	Service  string
	Env      string
	Stage    string // which job: compile / package / notify
	Commit   string
	Pipeline string // link back to GitLab
	Actor    string
	Reason   string // the commit title
	Detail   string // the failing job's last lines, or the warning
}

func (m Message) text() string {
	switch {
	case m.Build != nil:
		return BuildText(m.Build, m.Event, m.System)
	case m.Release != nil:
		return Text(m.Release, m.Event, m.System)
	}
	return m.Text
}

// BuildText renders a build outcome for the channels that take plain text.
func BuildText(b *Build, event string, sys settings.System) string {
	var w strings.Builder
	fmt.Fprintf(&w, "[%s] %s → %s  %s\n", sys.SiteName, b.Service, b.Env, t(eventText[event]))
	if b.Stage != "" {
		fmt.Fprint(&w, t("n.fieldStage")+b.Stage+"\n")
	}
	if b.Actor != "" {
		fmt.Fprint(&w, t("n.fieldCreator")+b.Actor+"\n")
	}
	if b.Reason != "" {
		fmt.Fprint(&w, t("n.fieldReason")+b.Reason+"\n")
	}
	if b.Detail != "" {
		fmt.Fprintf(&w, "%s\n", clip(b.Detail, 800))
	}
	if b.Pipeline != "" {
		fmt.Fprint(&w, b.Pipeline)
	}
	return w.String()
}

// clip cuts to n runes, not bytes: a channel's limit is not a reason to send
// half a character.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (n *Notifier) send(ctx context.Context, ch settings.Channel, msg Message) error {
	body, err := payload(ch, msg, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 300 {
		return &upstream.HTTPError{Method: http.MethodPost, URL: redactURL(ch.URL), Status: resp.StatusCode, Body: string(b)}
	}
	// Lark answers HTTP 200 with a non-zero code on errors (bad signature, keyword).
	if ch.Kind == "lark" {
		var r struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(b, &r) == nil && r.Code != 0 {
			return fmt.Errorf("lark: code %d: %s", r.Code, r.Msg)
		}
	}
	return nil
}

// redactURL keeps scheme and host: webhook paths carry the credential.
func redactURL(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return "webhook"
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host + "/…"
}

func payload(ch settings.Channel, msg Message, now time.Time) ([]byte, error) {
	switch ch.Kind {
	case "teams":
		return json.Marshal(map[string]string{"text": strings.ReplaceAll(msg.text(), "\n", "\n\n")})
	case "lark":
		body := map[string]any{"msg_type": "text", "content": map[string]string{"text": msg.text()}}
		if card := LarkCard(msg); card != nil {
			body = map[string]any{"msg_type": "interactive", "card": card}
		}
		if ch.Secret != "" {
			ts := strconv.FormatInt(now.Unix(), 10)
			body["timestamp"], body["sign"] = ts, LarkSign(ts, ch.Secret)
		}
		return json.Marshal(body)
	default: // webhook
		return json.Marshal(map[string]string{"text": msg.text()})
	}
}

// Card colours: waiting for approval stands out, results read at a glance.
// Orange is for the two cards that ask somebody to do something; the rest
// only report.
var cardColor = map[string]string{
	EventPending: "orange", EventApproval: "orange", EventStarted: "blue", EventSucceeded: "green",
	EventFailed: "red", EventRejected: "red", EventCancelled: "grey",
	EventBuildFailed: "red", EventBuildWarning: "orange",
}

// LarkCard renders a release event as a Lark interactive card with a button
// back to Tide (approvers land straight on the release). Plain text messages
// (the channel test) return nil and go out as text.
func LarkCard(msg Message) map[string]any {
	if msg.Build != nil {
		return larkBuildCard(msg)
	}
	r := msg.Release
	if r == nil {
		return nil
	}
	color := cardColor[msg.Event]
	if color == "" {
		color = "blue"
	}
	link := ""
	if msg.System.BaseURL != "" {
		link = strings.TrimRight(msg.System.BaseURL, "/") + "/releases/" + r.ID
	}
	jira := r.JiraTicket
	if jira == "" {
		jira = "—"
	}
	fields := []any{
		larkField(t("n.fieldEnv") + r.Env),
		larkField(t("n.fieldCreator") + r.CreatedByName),
		larkField("**Jira**\n" + jira),
		larkField(t("n.fieldRelease") + r.ID),
	}
	elements := []any{map[string]any{"tag": "div", "fields": fields}}
	if lines := itemLines(r); lines != "" {
		elements = append(elements, map[string]any{"tag": "div", "text": larkText(lines)})
	}
	if r.Reason != "" {
		elements = append(elements, map[string]any{"tag": "div", "text": larkText(t("n.fieldReason") + r.Reason)})
	}
	if msg.Event == EventApproval && r.ApprovalRule != nil {
		elements = append(elements, map[string]any{"tag": "div", "text": larkText(t("n.fieldApproval") + approvalLine(r))})
	}
	if link != "" {
		// The button says what is wanted, not just where it goes: these two
		// cards land in a channel precisely because somebody must act.
		label := t("n.viewRelease")
		switch msg.Event {
		case EventApproval:
			label = t("n.goApprove")
		case EventPending:
			label = t("n.goConfirm")
		}
		elements = append(elements, map[string]any{"tag": "action", "actions": []any{map[string]any{
			"tag": "button", "type": "primary", "url": link,
			"text": map[string]string{"tag": "plain_text", "content": label},
		}}})
	}
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": color,
			// t(), not the key: eventText holds catalog keys, and printing one
			// straight into the title puts "n.eventPending" on the card.
			"title": map[string]string{"tag": "plain_text", "content": fmt.Sprintf("%s · %s", subjectOf(r), t(eventText[msg.Event]))},
		},
		"elements": elements,
	}
}

// larkBuildCard is the build outcome's card. The button goes to GitLab, not
// to Tide: there is no release to look at, and the pipeline log is what the
// person reading this needs.
func larkBuildCard(msg Message) map[string]any {
	b := msg.Build
	color := cardColor[msg.Event]
	if color == "" {
		color = "red"
	}
	stage := b.Stage
	if stage == "" {
		stage = "—"
	}
	fields := []any{
		larkField(t("n.fieldEnv") + b.Env),
		larkField(t("n.fieldStage") + stage),
	}
	if b.Actor != "" {
		fields = append(fields, larkField(t("n.fieldCreator")+b.Actor))
	}
	if b.Commit != "" {
		fields = append(fields, larkField("**Commit**\n"+clip(b.Commit, 12)))
	}
	elements := []any{map[string]any{"tag": "div", "fields": fields}}
	if b.Reason != "" {
		elements = append(elements, map[string]any{"tag": "div", "text": larkText(t("n.fieldReason") + b.Reason)})
	}
	if b.Detail != "" {
		elements = append(elements, map[string]any{"tag": "div", "text": larkText("```\n" + clip(b.Detail, 800) + "\n```")})
	}
	if b.Pipeline != "" {
		elements = append(elements, map[string]any{"tag": "action", "actions": []any{map[string]any{
			"tag": "button", "type": "primary", "url": b.Pipeline,
			"text": map[string]string{"tag": "plain_text", "content": t("n.viewPipeline")},
		}}})
	}
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": color,
			"title":    map[string]string{"tag": "plain_text", "content": fmt.Sprintf("%s · %s", b.Service, t(eventText[msg.Event]))},
		},
		"elements": elements,
	}
}

// larkField is a half-width field; four of them make two rows.
func larkField(md string) map[string]any {
	return map[string]any{"is_short": true, "text": larkText(md)}
}

func larkText(md string) map[string]string {
	return map[string]string{"tag": "lark_md", "content": md}
}

// subjectOf is the release title, with the environment appended unless the
// generated title already ends in it.
func subjectOf(r *release.Release) string {
	if strings.HasSuffix(r.Title, " "+r.Env) {
		return r.Title
	}
	return r.Title + " → " + r.Env
}

// itemLines renders one line per item, the same content as the text message.
func itemLines(r *release.Release) string {
	var b strings.Builder
	for _, it := range r.Items {
		switch it.Kind {
		case release.KindRestart:
			if p, err := r.RestartPayload(it); err == nil {
				fmt.Fprint(&b, t("n.itemRestart", p.Service, label(&p.Current)))
			}
		case release.KindSync:
			if p, err := r.SyncPayload(it); err == nil {
				fmt.Fprint(&b, t("n.itemSync", p.Service, len(p.Changes)))
			}
		default:
			if p, err := r.ImagePayload(it); err == nil {
				fmt.Fprintf(&b, "· %s %s → **%s**\n", p.Service, label(p.From), label(&p.To))
			}
		}
		if it.Error != "" {
			e := []rune(it.Error)
			if len(e) > 200 {
				e = append(e[:200], '…')
			}
			fmt.Fprintf(&b, "  %s\n", string(e))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func approvalLine(r *release.Release) string {
	rule := r.ApprovalRule
	mode := t("n.modeAny")
	switch rule.Mode {
	case release.ApproveAll:
		mode = t("n.modeAll")
	case release.ApproveCount:
		mode = t("n.modeAtLeast", rule.MinApprovals)
	}
	return t("n.approvalLine", rule.Name, mode, strings.Join(rule.Approvers, ", "))
}

// LarkSign implements the Lark custom bot signature: HMAC-SHA256 keyed with
// "timestamp\nsecret" over an empty message, base64 encoded.
func LarkSign(timestamp, secret string) string {
	h := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Text renders a release event as plain text.
func Text(r *release.Release, event string, sys settings.System) string {
	var b strings.Builder
	jira := r.JiraTicket
	if jira == "" {
		jira = "—"
	}
	// The title already ends in the environment ("svc → uat", "svc restart @ uat"),
	// so only add it when it does not.
	subject := r.Title
	if !strings.HasSuffix(subject, " "+r.Env) {
		subject += " → " + r.Env
	}
	fmt.Fprint(&b, t("n.plainHeader", sys.SiteName, r.ID, subject, t(eventText[event]), jira, r.CreatedByName))
	for _, it := range r.Items {
		if p, err := r.RestartPayload(it); err == nil {
			fmt.Fprint(&b, t("n.itemRestartStatus", p.Service, label(&p.Current), it.Status))
		} else if p, err := r.ImagePayload(it); err == nil {
			fmt.Fprintf(&b, "· %s %s → %s [%s]\n", p.Service, label(p.From), label(&p.To), it.Status)
		} else {
			continue
		}
		if it.Error != "" {
			msg := it.Error
			if r := []rune(msg); len(r) > 300 {
				msg = string(r[:300]) + "…"
			}
			fmt.Fprintf(&b, "  %s\n", msg)
		}
	}
	if sys.BaseURL != "" {
		fmt.Fprintf(&b, "%s/releases/%s", strings.TrimRight(sys.BaseURL, "/"), r.ID)
	}
	return b.String()
}

func label(a *release.Artifact) string {
	if a == nil {
		return t("n.firstTime")
	}
	if a.Version != "" {
		return a.Version
	}
	return a.Tag
}
