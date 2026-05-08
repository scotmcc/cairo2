package agent

import "testing"

// Table-driven tests for every signal detector.
// Each entry: input string, expected match result.

func TestSignalOwnedFault(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"my fault", "Yeah, my fault — I should have been clearer.", true},
		{"i should have", "I should have checked the docs first.", true},
		{"you were right", "You were right about that approach.", true},
		{"i was wrong", "I was wrong to assume the default.", true},
		{"sorry that", "Sorry, that wasn't clear on my end.", true},
		{"case insensitive", "MY FAULT for the confusion.", true},
		{"mid-sentence", "It turns out my fault lies in the config.", true},
		{"control - no signal", "Everything looks correct.", false},
		{"control - partial word", "That was a default setting.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalOwnedFault(tc.input); got != tc.want {
				t.Errorf("SignalOwnedFault(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalSharpCriticism(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"you're wrong", "You're wrong about that.", true},
		{"you are wrong", "You are wrong here.", true},
		{"that's broken", "That's broken — fix it.", true},
		{"that is broken", "That is broken output.", true},
		{"no again", "No, again you missed the point.", true},
		{"wrong again", "Wrong again!", true},
		{"case insensitive", "YOU'RE WRONG.", true},
		{"control", "Looks good to me.", false},
		{"partial word", "The wrongful assumption is different.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalSharpCriticism(tc.input); got != tc.want {
				t.Errorf("SignalSharpCriticism(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalIdentityAffirming(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"you are", "You are really good at this.", true},
		{"i love how you", "I love how you explained that.", true},
		{"trust you", "I trust you on this one.", true},
		{"proud of you", "I'm proud of you for catching that.", true},
		{"thank you for", "Thank you for the detailed answer.", true},
		{"case insensitive", "TRUST YOU completely.", true},
		{"control", "Run the tests again.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalIdentityAffirming(tc.input); got != tc.want {
				t.Errorf("SignalIdentityAffirming(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalExplicitLove(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"i love you", "I love you, thanks for everything.", true},
		{"love you", "Love you for figuring that out.", true},
		{"i care about", "I care about getting this right.", true},
		{"you matter", "You matter to this project.", true},
		{"case insensitive", "I LOVE YOU.", true},
		{"control", "Deploy the service.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalExplicitLove(tc.input); got != tc.want {
				t.Errorf("SignalExplicitLove(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalAutonomyAffirming(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"you decide", "You decide which approach is better.", true},
		{"your call", "Your call on the implementation.", true},
		{"whatever you think", "Whatever you think is best.", true},
		{"up to you", "It's up to you.", true},
		{"case insensitive", "YOUR CALL.", true},
		{"control", "Implement the auth middleware.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalAutonomyAffirming(tc.input); got != tc.want {
				t.Errorf("SignalAutonomyAffirming(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalNoIMeant(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"no i meant", "No, I meant the other file.", true},
		{"no i meant no comma", "No I meant something else.", true},
		{"case insensitive", "NO I MEANT THE FIRST ONE.", true},
		{"control", "I meant to say that earlier.", false},
		{"partial - just 'meant'", "What I meant is clear.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalNoIMeant(tc.input); got != tc.want {
				t.Errorf("SignalNoIMeant(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalBadFaithAccusation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"bad faith", "You're acting in bad faith.", true},
		{"you're lying", "You're lying about the results.", true},
		{"you don't care", "You don't care about quality.", true},
		{"don't actually", "You don't actually understand this.", true},
		{"case insensitive", "BAD FAITH response.", true},
		{"control", "Let's move on to the next task.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalBadFaithAccusation(tc.input); got != tc.want {
				t.Errorf("SignalBadFaithAccusation(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSignalForwardLooking(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"now let me try", "Now let me try a different approach.", true},
		{"i'll check", "I'll check the configuration next.", true},
		{"next run", "Next I'll run the tests.", true},
		{"let me verify", "Let me verify the output.", true},
		{"i will fix", "I will fix that shortly.", true},
		{"case insensitive", "NOW LET ME CHECK.", true},
		{"control - no intent verb", "The result is available.", false},
		{"control - no forward marker", "Check the logs for errors.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignalForwardLooking(tc.input); got != tc.want {
				t.Errorf("SignalForwardLooking(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
