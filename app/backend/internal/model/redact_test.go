package model

import "testing"

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://integrations.zoom.us/chat/webhooks/incomingwebhook/abcdWXYZ?format=full":                       "https://integrations.zoom.us/…WXYZ",
		"https://prod-01.westeurope.logic.azure.com:443/workflows/1/triggers/manual/paths/invoke?sp=x&sig=QwEr": "https://prod-01.westeurope.logic.azure.com:443/…QwEr",
		"https://example.com/": "https://example.com/…",
		"not a url":            "…",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
