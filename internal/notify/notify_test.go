package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tide/internal/release"
	"tide/internal/settings"
)

func names(cs []settings.Channel) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestTargets(t *testing.T) {
	cfg := settings.Notify{
		Channels: []settings.Channel{
			{Name: "ops", Enabled: true},
			{Name: "dev", Enabled: true},
			{Name: "off", Enabled: false},
		},
		Rules: []settings.NotifyRule{
			{Name: "prod all", Enabled: true, Envs: []string{"tier:production"}, Events: Events, Channels: []string{"ops", "off"}},
			{Name: "failures", Enabled: true, Envs: []string{"*"}, Events: []string{EventFailed}, Channels: []string{"dev", "ops"}},
			{Name: "disabled", Enabled: false, Envs: []string{"*"}, Events: Events, Channels: []string{"dev"}},
		},
	}
	tests := []struct {
		env, tier, event string
		want             []string
	}{
		{"prod", "production", EventStarted, []string{"ops"}},
		{"prod", "production", EventFailed, []string{"ops", "dev"}},
		{"dev", "development", EventStarted, []string{}},
		{"dev", "development", EventFailed, []string{"ops", "dev"}},
	}
	for _, tt := range tests {
		got := names(Targets(cfg, tt.env, tt.tier, tt.event, "cart"))
		if len(got) != len(tt.want) {
			t.Fatalf("%s/%s: got %v, want %v", tt.env, tt.event, got, tt.want)
		}
		for _, w := range tt.want {
			found := false
			for _, g := range got {
				found = found || g == w
			}
			if !found {
				t.Fatalf("%s/%s: got %v, want %v", tt.env, tt.event, got, tt.want)
			}
		}
	}
}

func TestLarkPayloadSigned(t *testing.T) {
	now := time.Unix(1700000000, 0)
	b, err := payload(settings.Channel{Kind: "lark", Secret: "s3cret"}, Message{Text: "hi"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["timestamp"] != "1700000000" || m["sign"] != LarkSign("1700000000", "s3cret") || m["sign"] == "" {
		t.Fatalf("payload = %s", b)
	}
	b, _ = payload(settings.Channel{Kind: "lark"}, Message{Text: "hi"}, now)
	var unsigned map[string]any
	if err := json.Unmarshal(b, &unsigned); err != nil {
		t.Fatal(err)
	}
	if _, signed := unsigned["sign"]; signed {
		t.Fatalf("unsigned channel got a signature: %s", b)
	}
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("https://open.larksuite.com/open-apis/bot/v2/hook/abc-token"); got != "https://open.larksuite.com/…" {
		t.Fatalf("got %s", got)
	}
}

func TestTextDoesNotRepeatTheEnvironment(t *testing.T) {
	sys := settings.System{SiteName: "Tide", BaseURL: "https://tide.example.com/"}
	rel := func(title, env string) *release.Release {
		return &release.Release{ID: "REL-1", Title: title, Env: env, CreatedByName: "Dev1"}
	}
	for name, tc := range map[string]struct {
		r    *release.Release
		want string
	}{
		"title ends in the env": {rel("svc → uat", "uat"), "[Tide] REL-1 svc → uat 开始执行"},
		"restart title":         {rel("svc 重启 @ uat", "uat"), "[Tide] REL-1 svc 重启 @ uat 开始执行"},
		"title without env":     {rel("回滚网关链路", "prod"), "[Tide] REL-1 回滚网关链路 → prod 开始执行"},
	} {
		got, _, _ := strings.Cut(Text(tc.r, EventStarted, sys), "\n")
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
	if !strings.HasSuffix(Text(rel("svc → uat", "uat"), EventStarted, sys), "https://tide.example.com/releases/REL-1") {
		t.Error("the message must end with the release link")
	}
}

// A card that asks somebody to act must look different from one that only
// reports, and its button must say what is wanted.
func TestCardsThatNeedSomebodyLookUrgent(t *testing.T) {
	rel := &release.Release{ID: "REL-20260921-004", Env: "uat", CreatedByName: "acme-ci", Status: release.Confirming}
	sys := settings.System{BaseURL: "https://tide.example.com"}
	tests := []struct {
		event, color, button, title string
	}{
		{EventPending, "orange", "去确认", "等待确认"},
		{EventApproval, "orange", "去审批", "等待审批"},
		{EventStarted, "blue", "查看发布单", "开始执行"},
		{EventSucceeded, "green", "查看发布单", "成功"},
		{EventFailed, "red", "查看发布单", "失败"},
	}
	for _, tt := range tests {
		card := LarkCard(Message{Release: rel, Event: tt.event, System: sys})
		if card == nil {
			t.Fatalf("%s: no card", tt.event)
		}
		header, _ := card["header"].(map[string]any)
		if got := header["template"]; got != tt.color {
			t.Errorf("%s: colour %v, want %s", tt.event, got, tt.color)
		}
		if got := buttonLabel(card); got != tt.button {
			t.Errorf("%s: button %q, want %q", tt.event, got, tt.button)
		}
		// The title is words, never a catalog key: eventText holds keys and
		// one of them went out on a card unrendered.
		title, _ := header["title"].(map[string]string)
		if got := title["content"]; !strings.HasSuffix(got, " · "+tt.title) {
			t.Errorf("%s: title %q, want it to end in %q", tt.event, got, tt.title)
		}
	}
}

// buttonLabel digs out the card's action button text.
func buttonLabel(card map[string]any) string {
	elements, _ := card["elements"].([]any)
	for _, e := range elements {
		el, _ := e.(map[string]any)
		if el["tag"] != "action" {
			continue
		}
		actions, _ := el["actions"].([]any)
		for _, a := range actions {
			btn, _ := a.(map[string]any)
			text, _ := btn["text"].(map[string]string)
			return text["content"]
		}
	}
	return ""
}

// A rule scoped to some services must not pick up the others, and a rule
// that names none must keep meaning "all of them" — every rule written
// before the selector existed says nothing about services.
func TestAServiceSelectorNarrowsARuleWithoutNarrowingTheOnesThatHaveNone(t *testing.T) {
	cfg := settings.Notify{
		Channels: []settings.Channel{
			{Name: "everyone", Kind: "lark", URL: "https://example.com/a", Enabled: true},
			{Name: "cart-team", Kind: "lark", URL: "https://example.com/b", Enabled: true},
		},
		Rules: []settings.NotifyRule{
			{Name: "all", Enabled: true, Envs: []string{"*"}, Events: []string{EventBuildFailed}, Channels: []string{"everyone"}},
			{Name: "cart", Enabled: true, Envs: []string{"*"}, Events: []string{EventBuildFailed},
				Channels: []string{"cart-team"}, Services: []string{"cart-*"}},
		},
	}
	for service, want := range map[string][]string{
		"cart-api":    {"everyone", "cart-team"},
		"portal-api":  {"everyone"},
		"cart-worker": {"everyone", "cart-team"},
	} {
		got := names(Targets(cfg, "uat", "", EventBuildFailed, service))
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", service, got, want)
		}
	}
}

func TestServiceMatches(t *testing.T) {
	for _, tc := range []struct {
		selectors []string
		service   string
		want      bool
	}{
		{nil, "anything", true},
		{[]string{"*"}, "anything", true},
		{[]string{"cart-api"}, "cart-api", true},
		{[]string{"cart-api"}, "cart-apix", false},
		{[]string{"cart-*"}, "cart-api", true},
		{[]string{"cart-*"}, "portal-api", false},
		{[]string{"a-*", "b-*"}, "b-one", true},
		// A rule that names services cannot match a release that is about
		// several of them, which arrives here as no service at all.
		{[]string{"cart-*"}, "", false},
	} {
		if got := ServiceMatches(tc.selectors, tc.service); got != tc.want {
			t.Errorf("ServiceMatches(%v, %q) = %v", tc.selectors, tc.service, got)
		}
	}
}

// A build failure has no release to link to, so its card has to stand on its
// own and send the reader to the pipeline instead.
func TestABuildFailureCardPointsAtThePipeline(t *testing.T) {
	msg := Message{
		Event:  EventBuildFailed,
		System: settings.System{SiteName: "Tide", BaseURL: "https://tide.example.com"},
		Build: &Build{
			Service: "cart-api", Env: "uat", Stage: "compile", Commit: "abc1234",
			Pipeline: "https://git.example.com/pipelines/42", Actor: "someone",
			Reason: "fix rounding", Detail: "undefined: total",
		},
	}
	card := LarkCard(msg)
	if card == nil {
		t.Fatal("a build event must render as a card, not fall through to plain text")
	}
	blob, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cart-api", "compile", "undefined: total", "https://git.example.com/pipelines/42"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("card omits %q: %s", want, blob)
		}
	}
	// The release card's link would be wrong here, and worse than no link.
	if strings.Contains(string(blob), "tide.example.com/releases/") {
		t.Error("there is no release to link to")
	}
	if txt := msg.text(); !strings.Contains(txt, "cart-api") || !strings.Contains(txt, "compile") {
		t.Errorf("plain text must carry the same: %q", txt)
	}
}
