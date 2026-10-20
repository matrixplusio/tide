package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

const oid = "49c4641c-ab91-4248-aebb-6a7de286397b"

func teamsJSON(t *testing.T, msg Message) (string, map[string]any) {
	t.Helper()
	b, err := json.Marshal(TeamsMessage(msg))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return string(b), m["attachments"].([]any)[0].(map[string]any)["content"].(map[string]any)
}

// A Teams card mentions by Microsoft Entra object id: an <at> tag in the
// text, an entity naming the id. Anything not shaped like one is left out.
func TestATeamsCardMentionsByEntraObjectID(t *testing.T) {
	r := &release.Release{ID: "REL-1", Env: "uat", Title: "order → uat", CreatedByName: "Alice",
		ApprovalRule: &release.ApprovalRule{Name: "uat", Approvers: []string{"group:Ops"}}}
	raw, card := teamsJSON(t, Message{Release: r, Event: EventApproval, System: settings.System{BaseURL: "https://tide.example.com"},
		Mentions: []pg.Mention{{TeamsID: oid, Name: "Bob <b>"}, {ID: "ee11aa22", Name: "Lark only"}, {TeamsID: "29:abc", Name: "Not an oid"}}})
	ents := card["msteams"].(map[string]any)["entities"].([]any)
	if len(ents) != 1 {
		t.Fatalf("entities %v, want only Bob", ents)
	}
	e := ents[0].(map[string]any)
	if e["text"] != "<at>Bob b</at>" || e["mentioned"].(map[string]any)["id"] != oid {
		t.Errorf("entity %v", e)
	}
	found := false
	for _, b := range card["body"].([]any) {
		if b.(map[string]any)["text"] == "<at>Bob b</at>" {
			found = true
		}
	}
	if !found {
		t.Errorf("the card text does not carry the mention tag: %s", raw)
	}
	if !strings.Contains(raw, "https://tide.example.com/releases/REL-1") || !strings.Contains(raw, "Action.OpenUrl") {
		t.Errorf("no button back to the release: %s", raw)
	}
}

// A batch large enough to pass the 28 KB Teams takes is cut short, and says so.
func TestALargeBatchStillFitsATeamsCard(t *testing.T) {
	r := &release.Release{ID: "REL-2", Env: "qa", Title: "batch → qa"}
	for i := 0; i < 400; i++ {
		p, _ := json.Marshal(release.ImagePayload{Service: fmt.Sprintf("service-with-a-long-name-%03d", i), Env: "qa",
			To: release.Artifact{Version: "v1.0.0", Tag: "20260917150452-938efb5c-0006"}})
		r.Items = append(r.Items, release.Item{Kind: release.KindImage, Payload: p, Status: release.ItemSucceeded})
	}
	raw, _ := teamsJSON(t, Message{Release: r, Event: EventSucceeded})
	if len(raw) > 28<<10 {
		t.Fatalf("card is %d bytes, over the Teams limit", len(raw))
	}
	if !strings.Contains(raw, "没有列出") && !strings.Contains(raw, "not shown") {
		t.Errorf("the card does not say lines were left out")
	}
}

func TestTeamsBuildAndTestCards(t *testing.T) {
	raw, _ := teamsJSON(t, Message{Build: &Build{Service: "order", Env: "qa", Image: "registry.example.com/shop/order:20260917150452-938efb5c-0006"}, Event: EventBuildReady})
	if !strings.Contains(raw, "20260917150452-938efb5c-0006") {
		t.Errorf("build card lacks the tag: %s", raw)
	}
	_, card := teamsJSON(t, Message{Test: true, Text: "test", System: settings.System{SiteName: "Tide"}, Mentions: []pg.Mention{{TeamsID: oid, Name: "Erin"}}})
	if ents := card["msteams"].(map[string]any)["entities"].([]any); len(ents) != 1 {
		t.Errorf("the test card does not mention the tester: %v", ents)
	}
}
