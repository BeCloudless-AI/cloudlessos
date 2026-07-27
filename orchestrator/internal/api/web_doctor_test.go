package api

import (
	"strings"
	"testing"
)

func TestCloudlessDoctorIsAvailableInSettings(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{
		"Cloudless Doctor", "/api/system/doctor", "/api/system/doctor/bundle",
		"Tokens, passwords, API-key hashes, hostnames, and home paths are removed",
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("Doctor UI is missing %q", required)
		}
	}
}

func TestOptionalCapabilityPacksAreNativeLauncherUI(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{"Optional Cloudless packs", "/api/packs", "installPack", "Pack installation rolled back"} {
		if !strings.Contains(page, required) {
			t.Fatalf("pack UI is missing %q", required)
		}
	}
}
