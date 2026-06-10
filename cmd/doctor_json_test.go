package cmd

import (
	"testing"

	"github.com/grovetools/core/pkg/doctor"
)

func TestToDoctorJSON_StatusMapping(t *testing.T) {
	results := []doctor.CheckResult{
		{ID: "a", Status: doctor.StatusOK, Message: "fine"},
		{ID: "b", Status: doctor.StatusWarn, Message: "meh"},
		{ID: "c", Status: doctor.StatusFail, Message: "broken", Resolution: "fix it", Error: "boom"},
	}

	out := toDoctorJSON(results)
	if len(out) != 3 {
		t.Fatalf("expected 3 results, got %d", len(out))
	}
	want := []struct{ check, status, detail string }{
		{"a", "pass", "fine"},
		{"b", "warn", "meh"},
		{"c", "fail", "broken"},
	}
	for i, w := range want {
		if out[i].Check != w.check || out[i].Status != w.status || out[i].Detail != w.detail {
			t.Errorf("result %d = %+v, want %+v", i, out[i], w)
		}
	}
	if out[2].Resolution != "fix it" || out[2].Error != "boom" {
		t.Errorf("fail result missing resolution/error: %+v", out[2])
	}
}
