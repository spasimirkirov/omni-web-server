package main

import (
	"os"
	"testing"
)

// TestTemplatesLive fetches the real version lists. Run with OMNI_LIVE=1.
func TestTemplatesLive(t *testing.T) {
	if os.Getenv("OMNI_LIVE") == "" {
		t.Skip("set OMNI_LIVE=1 to query Packagist and wordpress.org")
	}
	for _, tpl := range (&App{}).templates(true) {
		if tpl.Error != "" {
			t.Errorf("%s: %s", tpl.ID, tpl.Error)
		}
		t.Logf("%-16s %v", tpl.ID, tpl.Versions)
	}
}
