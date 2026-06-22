package completeness

import "testing"

func TestInferProjectType(t *testing.T) {
	cases := []struct{ intent, want string }{
		{"Build an HTTP service that returns the current time", "http-service"},
		{"Build a JSON API on port 8080", "http-service"},
		{"Build a CLI tool that prints the time", "cli"},
		{"Write a command-line utility", "cli"},
		{"Build a reusable Go library for date parsing", "library"},
		{"Create an SDK for our service", "library"},
		{"Build a background worker that processes jobs", "worker"},
		{"Run a cron job nightly", "worker"},
		{"Build a thing", "http-service"}, // bare idea → default; the gate never guesses
	}
	for _, c := range cases {
		if got := InferProjectType(c.intent); got != c.want {
			t.Errorf("InferProjectType(%q) = %q, want %q", c.intent, got, c.want)
		}
	}
}
