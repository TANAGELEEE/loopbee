package core

import (
	"os"
	"testing"
)

func TestCompletionCondition(t *testing.T) {
	for _, tc := range []struct {
		source         string
		metrics        map[string]float64
		verified, want bool
		bad            bool
	}{
		{"metrics.score >= 90 && verify.passed", map[string]float64{"score": 95}, true, true, false},
		{"metrics.score >= 90 && verify.passed", map[string]float64{"score": 95}, false, false, false},
		{"metrics.score != 90", nil, true, false, false},
		{"exec(\"rm\")", nil, true, false, true},
		{"metrics.score + 1 > 90", nil, true, false, true},
		{"1", nil, true, false, true},
		{"!verify.passed", nil, false, true, false},
	} {
		got, err := condition(tc.source, tc.metrics, tc.verified)
		if (err != nil) != tc.bad || got != tc.want {
			t.Fatalf("%s: %t %v", tc.source, got, err)
		}
	}
}
func TestVerifiedMetric(t *testing.T) {
	v, err := metricValue([]byte(`{"summary":{"score":92}}`), "$.summary.score")
	if err != nil || v != 92 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err = metricValue([]byte(`{"summary":{"score":"92"}}`), "$.summary.score"); err == nil {
		t.Fatal("string accepted as verified numeric metric")
	}
}

func TestMetricControlsCompletionAndStall(t *testing.T) {
	t.Setenv("LOOPBEE_HELPER", "metric")
	a := testApp(t)
	for _, target := range []struct{ expression, status string }{{"metrics.score >= 90 && verify.passed", "completed"}, {"metrics.score >= 100 && verify.passed", "stalled"}} {
		l := testLoop(t)
		l.MaxRounds = 3
		l.StallRounds = 1
		l.DoneWhen = target.expression
		l.AllowedTools = []string{os.Args[0]}
		l.Steps = []Step{{Kind: "verify", Name: "Measure", Argv: []string{os.Args[0], "-test.run=TestHelperProcess"}, Metric: "score", MetricPath: "$.summary.score"}}
		l, err := a.SaveLoop(l)
		if err != nil {
			t.Fatal(err)
		}
		r, err := a.Start(l.ID)
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, a, r.ID, target.status)
		r, _ = a.Run(r.ID)
		if r.Metrics["score"] != 92 {
			t.Fatal("verified metric not persisted")
		}
	}
}
