package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"tide/internal/i18n"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/store/pg"
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

// The test button exists to prove a channel works. If it goes out as plain
// text while every real notification goes out as a card, it proves the wrong
// thing: Lark can accept the text and then refuse the cards.
func TestTheChannelTestGoesOutInTheSameShapeAsARealOne(t *testing.T) {
	msg := Message{Test: true, Text: "channel ok", System: settings.System{SiteName: "Tide", BaseURL: "https://tide.example.com"}}
	card := LarkCard(msg)
	if card == nil {
		t.Fatal("the test message must render as a card")
	}
	blob, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"channel ok", "Tide", "https://tide.example.com"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("test card omits %q: %s", want, blob)
		}
	}
	// A channel with nowhere to point still has to render.
	if LarkCard(Message{Test: true, Text: "channel ok"}) == nil {
		t.Error("a deployment with no base URL still tests its channels")
	}
}

// imagePayload is the stored form of an image item, which is what the
// renderers read.
func imagePayload(service string) []byte {
	b, err := json.Marshal(release.ImagePayload{
		Service: service, Env: "dev",
		From: &release.Artifact{Tag: "old"},
		To:   release.Artifact{Tag: "new"},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// A batch of seven where one item failed is a failed release, which is true
// and, on its own, misleading: the word says the whole thing came to
// nothing. Someone reading it on their phone has to be able to tell "one
// service needs looking at" from "none of it worked".
func TestAPartlyFailedBatchSaysHowMuchOfItWorked(t *testing.T) {
	r := &release.Release{
		ID: "REL-1", Title: "batch → dev", Env: "dev", Status: release.Failed, CreatedByName: "someone",
		Items: []release.Item{
			{Kind: release.KindImage, Status: release.ItemSucceeded, Payload: imagePayload("a")},
			{Kind: release.KindImage, Status: release.ItemSucceeded, Payload: imagePayload("b")},
			{Kind: release.KindImage, Status: release.ItemFailed, Payload: imagePayload("c"), Error: "rollout failed"},
		},
	}
	sys := settings.System{SiteName: "Tide"}

	text := Text(r, EventFailed, sys)
	for _, want := range []string{"2 项成功", "1 项失败"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tally is missing %q from:\n%s", want, text)
		}
	}

	card := LarkCard(Message{Release: r, Event: EventFailed, System: sys})
	blob, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 项成功", "1 项失败"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("the card omits %q: %s", want, blob)
		}
	}
	// And each service carries its own verdict, so the one to look at is
	// named rather than inferred from which line has an error under it.
	for _, want := range []string{"[成功]", "[失败]"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("the card does not mark items with %q: %s", want, blob)
		}
	}
}

// One item is its own tally; repeating the release's status as "1 succeeded"
// is noise.
func TestASingleItemReleaseHasNoTally(t *testing.T) {
	r := &release.Release{
		ID: "REL-2", Title: "svc → dev", Env: "dev", Status: release.Succeeded, CreatedByName: "someone",
		Items: []release.Item{{Kind: release.KindImage, Status: release.ItemSucceeded, Payload: imagePayload("a")}},
	}
	if got := Text(r, EventSucceeded, settings.System{SiteName: "Tide"}); strings.Contains(got, "1 项成功") {
		t.Errorf("a single item should not be tallied:\n%s", got)
	}
}

// "Failed" on a batch where six of seven worked reads as though none did,
// and the title is all a phone shows before the message is opened.
func TestAPartlyFailedBatchSaysSoInTheTitle(t *testing.T) {
	mixed := &release.Release{
		ID: "REL-3", Title: "batch → dev", Env: "dev", Status: release.Failed, CreatedByName: "someone",
		Items: []release.Item{
			{Kind: release.KindImage, Status: release.ItemSucceeded, Payload: imagePayload("a")},
			{Kind: release.KindImage, Status: release.ItemFailed, Payload: imagePayload("b")},
		},
	}
	card, _ := json.Marshal(LarkCard(Message{Release: mixed, Event: EventFailed, System: settings.System{SiteName: "Tide"}}))
	if !strings.Contains(string(card), "部分失败") {
		t.Errorf("the title does not distinguish a partial failure: %s", card)
	}

	// All of it failing is not partial, and must keep saying so plainly.
	allBad := &release.Release{
		ID: "REL-4", Title: "batch → dev", Env: "dev", Status: release.Failed, CreatedByName: "someone",
		Items: []release.Item{
			{Kind: release.KindImage, Status: release.ItemFailed, Payload: imagePayload("a")},
			{Kind: release.KindImage, Status: release.ItemFailed, Payload: imagePayload("b")},
		},
	}
	card2, _ := json.Marshal(LarkCard(Message{Release: allBad, Event: EventFailed, System: settings.System{SiteName: "Tide"}}))
	if strings.Contains(string(card2), "部分失败") {
		t.Errorf("a wholly failed batch must not be called partial: %s", card2)
	}
}

// The status beside each service was the one English word in a Chinese
// message.
func TestItemStatusesAreTranslated(t *testing.T) {
	r := &release.Release{
		ID: "REL-5", Title: "batch → dev", Env: "dev", Status: release.Failed, CreatedByName: "someone",
		Items: []release.Item{
			{Kind: release.KindImage, Status: release.ItemSucceeded, Payload: imagePayload("a")},
			{Kind: release.KindImage, Status: release.ItemFailed, Payload: imagePayload("b")},
		},
	}
	text := Text(r, EventFailed, settings.System{SiteName: "Tide"})
	if strings.Contains(text, "[succeeded]") || strings.Contains(text, "[failed]") {
		t.Errorf("raw status words leaked into the message:\n%s", text)
	}
	if !strings.Contains(text, "[成功]") || !strings.Contains(text, "[失败]") {
		t.Errorf("translated statuses missing:\n%s", text)
	}
}

// In a batch, the services waiting for a switch are the ones the reader has to
// act on; both the card and the plain text name them as such.
func TestWaitingItemsAreNamedInBothMessages(t *testing.T) {
	now := time.Now()
	payload := func(svc string) json.RawMessage {
		b, _ := json.Marshal(release.ImagePayload{Service: svc, Env: "qa", To: release.Artifact{Tag: "20260102000000-bbbbbbbb-0002"}})
		return b
	}
	r := &release.Release{ID: "REL-1", Title: "batch → qa", Env: "qa", Items: []release.Item{
		{ID: 1, Kind: release.KindImage, Status: release.ItemExecuting, PausedSince: &now, Payload: payload("order-web")},
		{ID: 2, Kind: release.KindImage, Status: release.ItemExecuting, Payload: payload("cart-api")},
	}}
	want := t2("n.itemAwaitingSwitch")
	text := Text(r, EventSwitch, settings.System{})
	if !strings.Contains(text, "order-web") || !strings.Contains(text, "["+want+"]") {
		t.Errorf("text does not mark the waiting service:\n%s", text)
	}
	if n := strings.Count(text, "["+want+"]"); n != 1 {
		t.Errorf("only order-web waits; text marks %d items:\n%s", n, text)
	}
	card, _ := json.Marshal(LarkCard(Message{Release: r, Event: EventSwitch}))
	if !strings.Contains(string(card), want) {
		t.Errorf("card does not mark the waiting service: %s", card)
	}
}

func t2(k string) string { return t(i18n.Key(k)) }

// The tag is what people copy from a notification into the next release or
// a rollback; a version label alone used to hide it.
func TestAnArtifactIsShownWithItsTag(t *testing.T) {
	for _, c := range []struct {
		a    *release.Artifact
		want string
	}{
		{&release.Artifact{Version: "v1.4.0", Tag: "20260917150452-938efb5c-0006"}, "v1.4.0 · 20260917150452-938efb5c-0006"},
		{&release.Artifact{Tag: "20260917150452-938efb5c-0006"}, "20260917150452-938efb5c-0006"},
		{&release.Artifact{Version: "v1.4.0", Tag: "v1.4.0"}, "v1.4.0"},
	} {
		if got := label(c.a); got != c.want {
			t.Errorf("label(%+v) = %q, want %q", *c.a, got, c.want)
		}
	}
	for ref, want := range map[string]string{
		"registry.example.com/shop/order:20260917150452-938efb5c-0006":                  "20260917150452-938efb5c-0006",
		"registry.example.com:5000/shop/order":                                          "",
		"registry.example.com:5000/shop/order:v1.4.0@sha256:0123456789abcdef0123456789": "v1.4.0",
		"": "",
	} {
		if got := imageTag(ref); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestABuildNoticeCarriesItsTag(t *testing.T) {
	b := &Build{Service: "order", Env: "qa", Image: "registry.example.com/shop/order:20260917150452-938efb5c-0006"}
	if txt := BuildText(b, EventBuildReady, settings.System{SiteName: "Tide"}); !strings.Contains(txt, "20260917150452-938efb5c-0006") {
		t.Errorf("plain text lacks the tag:\n%s", txt)
	}
	card, _ := json.Marshal(larkBuildCard(Message{Build: b, Event: EventBuildReady}))
	if !strings.Contains(string(card), "20260917150452-938efb5c-0006") {
		t.Errorf("card lacks the tag: %s", card)
	}
	b.Image = ""
	if txt := BuildText(b, EventBuildReady, settings.System{SiteName: "Tide"}); strings.Contains(txt, "**") && strings.Contains(txt, "tag") {
		t.Errorf("a build without an image shows a tag line:\n%s", txt)
	}
}

type fakePeople struct {
	subs, groups []string
	out          []pg.Mention
}

func (f *fakePeople) LarkMentions(_ context.Context, subs, groups []string) ([]pg.Mention, error) {
	f.subs, f.groups = subs, groups
	return f.out, nil
}

// Whoever started a release from Tide is mentioned on every event of it,
// and the approvers too while it waits for approval. A release CI created
// mentions nobody.
func TestWhomAnEventIsFor(t *testing.T) {
	r := &release.Release{Source: release.SourceUI, CreatedBy: "oidc|alice", ApprovalRule: &release.ApprovalRule{Approvers: []string{"user:oidc|bob", "group:Ops", "bogus"}}}
	for event, want := range map[string][2][]string{
		EventApproval:  {{"oidc|alice", "oidc|bob"}, {"Ops"}},
		EventSwitch:    {{"oidc|alice"}, nil},
		EventFailed:    {{"oidc|alice"}, nil},
		EventSucceeded: {{"oidc|alice"}, nil},
		EventStarted:   {{"oidc|alice"}, nil},
		EventPending:   {{"oidc|alice"}, nil},
		EventCancelled: {{"oidc|alice"}, nil},
	} {
		subs, groups := whom(r, event)
		if !slices.Equal(subs, want[0]) || !slices.Equal(groups, want[1]) {
			t.Errorf("%s: subs %v groups %v, want %v %v", event, subs, groups, want[0], want[1])
		}
	}
	ci := &release.Release{Source: release.SourceCI, CreatedBy: "ci:token", ApprovalRule: r.ApprovalRule}
	for _, event := range []string{EventApproval, EventSucceeded, EventFailed} {
		if subs, groups := whom(ci, event); subs != nil || groups != nil {
			t.Errorf("a CI release mentions %v %v on %s", subs, groups, event)
		}
	}
}

// A rule can turn mentions off for its channels; a channel two rules reach
// mentions if either of them does, and rules written before the switch
// existed mention.
func TestARuleCanTurnMentionsOff(t *testing.T) {
	cfg := settings.Notify{Rules: []settings.NotifyRule{
		{Enabled: true, Envs: []string{"*"}, Events: []string{EventSucceeded}, Channels: []string{"quiet"}, NoMention: true},
		{Enabled: true, Envs: []string{"*"}, Events: []string{EventSucceeded}, Channels: []string{"loud"}},
		{Enabled: true, Envs: []string{"*"}, Events: []string{EventSucceeded}, Channels: []string{"both"}, NoMention: true},
		{Enabled: true, Envs: []string{"*"}, Events: []string{EventSucceeded}, Channels: []string{"both"}},
		{Enabled: false, Envs: []string{"*"}, Events: []string{EventSucceeded}, Channels: []string{"off"}},
	}}
	got := MentionChannels(cfg, "uat", "", EventSucceeded, "order")
	if !got["loud"] || !got["both"] || got["quiet"] || got["off"] {
		t.Errorf("mention channels %v, want loud and both only", got)
	}
}

// An id goes into the card's markup, so anything not shaped like a Lark id
// is dropped — whatever the store returned.
func TestMentionsAreOnlyLarkIDs(t *testing.T) {
	got := larkAts([]pg.Mention{{ID: "ou_0123abcd"}, {ID: "x></at><at id=all"}, {ID: ""}, {ID: "a1b2c3"}})
	if got != "<at id=ou_0123abcd></at> <at id=a1b2c3></at>" {
		t.Errorf("larkAts = %q", got)
	}
}

func TestTheApprovalCardMentionsTheApprovers(t *testing.T) {
	people := &fakePeople{out: []pg.Mention{{ID: "ou_bob", Name: "Bob"}, {ID: "bad id"}}}
	n := &Notifier{People: people}
	r := &release.Release{ID: "REL-1", Env: "uat", CreatedBy: "oidc|alice", Title: "order → uat",
		ApprovalRule: &release.ApprovalRule{Name: "uat", Approvers: []string{"group:Ops"}}}
	ms := n.mentions(context.Background(), r, EventApproval)
	if len(ms) != 1 || ms[0].ID != "ou_bob" || !slices.Equal(people.groups, []string{"Ops"}) {
		t.Fatalf("mentions %+v, asked for groups %v", ms, people.groups)
	}
	// Marshalled, < and > come out escaped as \u003c; Lark decodes them.
	card := fmt.Sprint(LarkCard(Message{Release: r, Event: EventApproval, Mentions: ms}))
	if !strings.Contains(card, "<at id=ou_bob></at>") {
		t.Errorf("approval card does not mention the approver: %s", card)
	}
	people.subs = nil
	n.mentions(context.Background(), r, EventSucceeded)
	if !slices.Equal(people.subs, []string{"oidc|alice"}) {
		t.Errorf("a success asks for %v, want the starter", people.subs)
	}
}
