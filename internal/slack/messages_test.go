package slack

import (
	"testing"
)

func TestBuildIntentMessage(t *testing.T) {
	tests := []struct {
		name    string
		persona string
		intent  string
		reason  string
		want    string
	}{
		{
			name:    "basic intent",
			persona: "Engineer-AE",
			intent:  "Investigating failing CI pipeline",
			reason:  "Blocking current development work",
			want:    "[Engineer-AE]\nIntent: Investigating failing CI pipeline\nReason: Blocking current development work",
		},
		{
			name:    "cto intent",
			persona: "CTO-AE",
			intent:  "Reviewing architecture proposal",
			reason:  "Needed before sprint planning",
			want:    "[CTO-AE]\nIntent: Reviewing architecture proposal\nReason: Needed before sprint planning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildIntentMessage(tt.persona, tt.intent, tt.reason)
			if got != tt.want {
				t.Errorf("BuildIntentMessage() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestBuildUpdateMessage(t *testing.T) {
	got := BuildUpdateMessage("Engineer-AE", "Identified issue in test configuration", "Preparing fix")
	want := "[Engineer-AE]\nUpdate: Identified issue in test configuration\nNext: Preparing fix"
	if got != want {
		t.Errorf("BuildUpdateMessage() = %q, want %q", got, want)
	}
}

func TestBuildBlockerMessage(t *testing.T) {
	t.Run("with target", func(t *testing.T) {
		got := BuildBlockerMessage("Engineer-AE", "Unclear which test framework to standardize on", "CTO-AE")
		want := "[Engineer-AE]\nBlocker: Unclear which test framework to standardize on\n@CTO-AE requesting guidance"
		if got != want {
			t.Errorf("BuildBlockerMessage() = %q, want %q", got, want)
		}
	})
	t.Run("without target", func(t *testing.T) {
		got := BuildBlockerMessage("Engineer-AE", "Build system is broken", "")
		want := "[Engineer-AE]\nBlocker: Build system is broken"
		if got != want {
			t.Errorf("BuildBlockerMessage() = %q, want %q", got, want)
		}
	})
}

func TestBuildRequestMessage(t *testing.T) {
	got := BuildRequestMessage("Engineer-AE", "CTO-AE", "Review PR #42 for CI fix")
	want := "[Engineer-AE → CTO-AE]\nRequest: Review PR #42 for CI fix"
	if got != want {
		t.Errorf("BuildRequestMessage() = %q, want %q", got, want)
	}
}

func TestBuildDecisionMessage(t *testing.T) {
	got := BuildDecisionMessage("CTO-AE", "Standardize on pytest", "Better ecosystem support")
	want := "[CTO-AE]\nDecision: Standardize on pytest\nReason: Better ecosystem support"
	if got != want {
		t.Errorf("BuildDecisionMessage() = %q, want %q", got, want)
	}
}
