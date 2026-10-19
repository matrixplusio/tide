package release

import (
	"encoding/json"
	"testing"
)

func TestOnlyAReleaseWithNothingToReadSkipsTheWait(t *testing.T) {
	image := func(anomalies []Anomaly, config []ResourceChange) Item {
		p, _ := json.Marshal(ImagePayload{Service: "order", Env: "uat", Anomalies: anomalies, ConfigChanges: config})
		return Item{Kind: KindImage, Payload: p}
	}
	restart := Item{Kind: KindRestart, Payload: json.RawMessage(`{"service":"order","env":"uat"}`)}
	for name, c := range map[string]struct {
		items []Item
		want  bool
	}{
		"plain upgrade":                 {[]Item{image(nil, nil)}, false},
		"restart":                       {[]Item{restart}, false},
		"first deploy is only a notice": {[]Item{image([]Anomaly{{Code: AnomalyFirstDeploy}}, nil)}, false},
		"rollback":                      {[]Item{image([]Anomaly{{Code: "rollback"}}, nil)}, true},
		"a code nobody has classified":  {[]Item{image([]Anomaly{{Code: "something_new"}}, nil)}, true},
		"config rides along":            {[]Item{image(nil, []ResourceChange{{Kind: "ConfigMap", Name: "order", Action: ChangeUpdate}})}, true},
		"one warned item in a batch":    {[]Item{image(nil, nil), restart, image([]Anomaly{{Code: "multi_version_jump"}}, nil)}, true},
		"config sync":                   {[]Item{{Kind: KindSync, Payload: json.RawMessage(`{}`)}}, true},
		"scale":                         {[]Item{{Kind: KindScale, Payload: json.RawMessage(`{}`)}}, true},
		"unreadable image payload":      {[]Item{{Kind: KindImage, Payload: json.RawMessage(`not json`)}}, true},
	} {
		r := &Release{Items: c.items}
		if got := r.NeedsReading(); got != c.want {
			t.Errorf("%s: NeedsReading = %v, want %v", name, got, c.want)
		}
	}
}
