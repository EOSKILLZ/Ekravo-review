package server

import "testing"

func TestProviderFrom(t *testing.T) {
	cases := []struct {
		base string
		name string
		link string
	}{
		{"https://ekravo.com/v1", "Ekravo", "https://ekravo.com"},
		{"https://www.ekravo.com/v1", "Ekravo", "https://ekravo.com"},
		{"https://gateway.ekravo.com/v1/", "Ekravo", "https://ekravo.com"},
		{"http://localhost:1234/v1", "localhost:1234", "http://localhost:1234"},
		{"https://api.example.com/v1", "api.example.com", "https://api.example.com"},
		{"", "Custom endpoint", ""},
		{"not a url", "Custom endpoint", ""},
	}
	for _, tc := range cases {
		name, link := providerFrom(tc.base)
		if name != tc.name || link != tc.link {
			t.Errorf("providerFrom(%q) = (%q, %q), want (%q, %q)", tc.base, name, link, tc.name, tc.link)
		}
	}
}
