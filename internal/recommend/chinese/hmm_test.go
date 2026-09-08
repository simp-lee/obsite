package chinese

import "testing"

func TestHMMViterbiPaths(t *testing.T) {
	model := newHMMModel()
	for _, test := range []struct {
		name         string
		observations string
		want         string
	}{
		{name: "known unknown word", observations: "杭研", want: "BE"},
		{name: "out of model tie breaking", observations: "㐀㐁㐂㐃", want: "SSSS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := string(model.viterbi([]rune(test.observations))); got != test.want {
				t.Fatalf("viterbi(%q) = %q, want %q", test.observations, got, test.want)
			}
		})
	}
}
