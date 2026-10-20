package notify

import (
	"encoding/json"
	"fmt"
	"strings"

	"tide/internal/store/pg"
)

// Teams takes an Adaptive Card in a message envelope, from an incoming
// webhook and a Power Automate workflow alike. Only a card can mention a
// person — by their Microsoft Entra object id — which plain text cannot, so
// every Teams message is a card and shows what the Lark one shows.

// teamsLimit keeps a card under the 28 KB Teams accepts from a webhook,
// with room for the envelope.
const teamsLimit = 26 << 10

// Adaptive Card colours for the Lark header templates the events use.
var teamsColor = map[string]string{"orange": "warning", "red": "attention", "green": "good", "blue": "accent", "grey": "default"}

// TeamsMessage renders msg as the body a Teams webhook takes.
func TeamsMessage(msg Message) map[string]any {
	return map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content":     teamsCard(msg),
		}},
	}
}

func teamsCard(msg Message) map[string]any {
	var title, color, link, button string
	var facts []any
	var body []any
	text := func(s string, extra map[string]any) map[string]any {
		b := map[string]any{"type": "TextBlock", "text": s, "wrap": true}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	fact := func(k, v string) map[string]any { return map[string]any{"title": k, "value": v} }
	switch {
	case msg.Test:
		title, color = t("n.testTitle", msg.System.SiteName), "accent"
		body = append(body, text(msg.Text, nil))
		link, button = strings.TrimRight(msg.System.BaseURL, "/"), msg.System.SiteName
	case msg.Build != nil:
		b := msg.Build
		title, color = fmt.Sprintf("%s · %s", b.Service, t(eventText[msg.Event])), teamsColor[cardColor[msg.Event]]
		facts = append(facts, fact(plain(t("n.fieldEnv")), b.Env))
		if b.Stage != "" {
			facts = append(facts, fact(plain(t("n.fieldStage")), b.Stage))
		}
		if b.Actor != "" {
			facts = append(facts, fact(plain(t("n.fieldCreator")), b.Actor))
		}
		if b.Commit != "" {
			facts = append(facts, fact("Commit", clip(b.Commit, 12)))
		}
		if tag := imageTag(b.Image); tag != "" {
			facts = append(facts, fact(plain(t("n.fieldTag")), tag))
		}
		if b.Reason != "" {
			body = append(body, text(t("n.fieldReason")+b.Reason, nil))
		}
		if b.Detail != "" {
			body = append(body, text(clip(b.Detail, 800), map[string]any{"fontType": "Monospace"}))
		}
		link, button = b.Pipeline, t("n.viewPipeline")
	case msg.Release != nil:
		r := msg.Release
		title, color = fmt.Sprintf("%s · %s", subjectOf(r), headline(r, msg.Event)), teamsColor[cardColor[msg.Event]]
		jira := r.JiraTicket
		if jira == "" {
			jira = "—"
		}
		facts = append(facts, fact(plain(t("n.fieldEnv")), r.Env), fact(plain(t("n.fieldCreator")), r.CreatedByName),
			fact("Jira", jira), fact(plain(t("n.fieldRelease")), r.ID))
		if sum := outcome(r); sum != "" {
			facts = append(facts, fact(plain(t("n.fieldOutcome")), sum))
		}
		for _, l := range strings.Split(itemLines(r), "\n") {
			if l != "" {
				body = append(body, text(l, map[string]any{"spacing": "None"}))
			}
		}
		if r.Reason != "" {
			body = append(body, text(t("n.fieldReason")+r.Reason, nil))
		}
		if msg.Event == EventApproval && r.ApprovalRule != nil {
			body = append(body, text(t("n.fieldApproval")+approvalLine(r), nil))
		}
		if msg.System.BaseURL != "" {
			link = strings.TrimRight(msg.System.BaseURL, "/") + "/releases/" + r.ID
		}
		button = t("n.viewRelease")
		switch msg.Event {
		case EventApproval:
			button = t("n.goApprove")
		case EventPending:
			button = t("n.goConfirm")
		case EventSwitch:
			button = t("n.goSwitch")
		}
	}
	if color == "" {
		color = "accent"
	}
	head := []any{text(title, map[string]any{"size": "Medium", "weight": "Bolder", "color": color})}
	ats, entities := teamsAts(msg.Mentions)
	if ats != "" {
		head = append(head, text(ats, nil))
	}
	if len(facts) > 0 {
		head = append(head, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"msteams": map[string]any{"width": "Full", "entities": entities},
	}
	if link != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": button, "url": link}}
	}
	// A large batch is cut short rather than refused: drop item lines from
	// the end until the card fits, and say how many were left out.
	dropped := 0
	for {
		card["body"] = append(append([]any{}, head...), body...)
		if dropped > 0 {
			card["body"] = append(card["body"].([]any), text(t("n.teamsTruncated", dropped), map[string]any{"isSubtle": true}))
		}
		if b, _ := json.Marshal(card); len(b) <= teamsLimit || len(body) == 0 {
			return card
		}
		body = body[:len(body)-1]
		dropped++
	}
}

// teamsAts renders mentions the way an Adaptive Card takes them: an <at>
// tag in the text, and an entity naming the Microsoft Entra object id behind
// it. Only ids shaped like one are written; the name loses angle brackets,
// which would end the tag.
func teamsAts(ms []pg.Mention) (string, []any) {
	var b strings.Builder
	entities := []any{}
	for _, m := range ms {
		if !pg.TeamsIDPattern.MatchString(m.TeamsID) {
			continue
		}
		name := strings.NewReplacer("<", "", ">", "").Replace(m.Name)
		if name == "" {
			name = m.TeamsID
		}
		tag := "<at>" + name + "</at>"
		fmt.Fprint(&b, tag+" ")
		entities = append(entities, map[string]any{"type": "mention", "text": tag,
			"mentioned": map[string]any{"id": m.TeamsID, "name": name}})
	}
	return strings.TrimSpace(b.String()), entities
}

// plain strips the markdown a field label carries for Lark ("**Env**\n").
func plain(s string) string {
	return strings.TrimSpace(strings.NewReplacer("**", "", "\n", "").Replace(s))
}
