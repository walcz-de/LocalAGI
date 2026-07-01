package agent

import "testing"

func TestGroundingResultOK(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   bool
	}{
		{"clean ok true", `{"ok": true, "flags": []}`, true},
		{"clean ok false", `{"ok": false, "flags": [{"severity":"high"}]}`, false},
		{"compact ok true", `{"ok":true}`, true},
		{"lenient wrapped ok true", `tool output: {"ok": true, "summary": {}} done`, true},
		{"garbage", `not json at all`, false},
		{"empty", ``, false},
		{"ok false substring", `{"ok": false}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := groundingResultOK(c.result); got != c.want {
				t.Fatalf("groundingResultOK(%q) = %v, want %v", c.result, got, c.want)
			}
		})
	}
}

func TestGroundingGate(t *testing.T) {
	const max = 3

	t.Run("not available -> allow", func(t *testing.T) {
		n := 0
		if _, blocked := groundingGate(false, false, &n, max); blocked {
			t.Fatal("grounding not available must not block")
		}
		if n != 0 {
			t.Fatalf("attempts must not change, got %d", n)
		}
	})

	t.Run("already passed -> allow", func(t *testing.T) {
		n := 0
		if _, blocked := groundingGate(true, true, &n, max); blocked {
			t.Fatal("passed grounding must not block")
		}
		if n != 0 {
			t.Fatalf("attempts must not change, got %d", n)
		}
	})

	t.Run("available and not passed -> block with adjustment", func(t *testing.T) {
		n := 0
		decision, blocked := groundingGate(true, false, &n, max)
		if !blocked {
			t.Fatal("must block until grounding passes")
		}
		if !decision.Approved {
			t.Fatal("must keep Approved=true so cogito re-runs selection (not abort the run)")
		}
		if decision.Adjustment == "" {
			t.Fatal("expected a non-empty adjustment nudging validate_grounding")
		}
		if n != 1 {
			t.Fatalf("expected attempt counter 1, got %d", n)
		}
	})

	t.Run("bounded: allows through after max attempts", func(t *testing.T) {
		n := 0
		for i := 0; i < max; i++ {
			if _, blocked := groundingGate(true, false, &n, max); !blocked {
				t.Fatalf("attempt %d should still block", i+1)
			}
		}
		if _, blocked := groundingGate(true, false, &n, max); blocked {
			t.Fatal("after max attempts the answer must be allowed through (no unbounded loop)")
		}
		if n != max {
			t.Fatalf("attempts should cap at %d, got %d", max, n)
		}
	})
}

func TestTextFinalizationNeedsGrounding(t *testing.T) {
	const max = 3
	cases := []struct {
		name                          string
		available, passed             bool
		attempts                      int
		role, content                 string
		want                          bool
	}{
		{"grounding unavailable", false, false, 0, "assistant", "answer", false},
		{"already passed", true, true, 0, "assistant", "answer", false},
		{"max attempts reached (graceful bypass)", true, false, max, "assistant", "answer", false},
		{"last message is a tool call, not text", true, false, 0, "tool", "", false},
		{"empty text answer", true, false, 0, "assistant", "   ", false},
		{"text finalization without grounding -> gate", true, false, 0, "assistant", "here is my answer", true},
		{"still under max -> gate", true, false, max - 1, "assistant", "answer", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := textFinalizationNeedsGrounding(c.available, c.passed, c.attempts, max, c.role, c.content); got != c.want {
				t.Fatalf("want %v, got %v", c.want, got)
			}
		})
	}
}
