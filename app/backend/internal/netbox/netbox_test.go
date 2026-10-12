package netbox

import "testing"

func TestNormalize(t *testing.T) {
	c, err := Config{URL: " https://netbox.example.com/api/ "}.Normalize()
	if err != nil || c.URL != "https://netbox.example.com" {
		t.Fatalf("normalize = %q %v", c.URL, err)
	}
	for _, bad := range []string{"", "netbox.example.com", "ftp://netbox", "https://user:pw@netbox", "https://netbox/?x=1"} {
		if _, err := (Config{URL: bad}).Normalize(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := (Config{URL: "https://n", SyncMinutes: -1}).Normalize(); err == nil {
		t.Error("negative interval accepted")
	}
	if got := c.ObjectURL(KindVM, 5); got != "https://netbox.example.com/virtualization/virtual-machines/5/" {
		t.Errorf("object URL = %s", got)
	}
}

func TestAuthorization(t *testing.T) {
	for token, want := range map[string]string{"abc": "Token abc", "nbt_key.secret": "Bearer nbt_key.secret"} {
		c, err := New(Config{URL: "https://n"}, token)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.authorization(); got != want {
			t.Errorf("%s: %s", token, got)
		}
	}
	if _, err := New(Config{URL: "https://n"}, " "); err == nil {
		t.Error("empty token accepted")
	}
}
