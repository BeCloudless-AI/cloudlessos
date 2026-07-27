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

func TestOptionalCapabilitiesAreNativeLauncherUI(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{"selectPack", "renderPackDetail", "Services included", "What Cloudless installs", "setLauncherActions", "/api/packs", "installPack", "Installation rolled back safely"} {
		if !strings.Contains(page, required) {
			t.Fatalf("optional capability UI is missing %q", required)
		}
	}
}

func TestEmbeddedAppsOpenThroughCloudlessOrigin(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "`${location.origin}${app.embeddedPath}`") || !strings.Contains(page, "appURL(app)") {
		t.Fatal("embedded app launcher does not use the Cloudless origin")
	}
}

func TestCapabilityFactsUseAlignedResponsiveGrid(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{
		"grid-template-columns: repeat(5,minmax(0,1fr))",
		"grid-template-rows: minmax(28px,auto) 1fr",
		"grid-template-columns:repeat(3,minmax(0,1fr))",
		"grid-template-columns:repeat(2,minmax(0,1fr))",
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("capability facts grid is missing %q", required)
		}
	}
}

func TestInstalledCapabilityAppsCanBePinned(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, required := range []string{
		`data-pack-action="pin"`,
		"toggleAppPin(app, button)",
		"toggleAppPin(a, btn)",
		"/api/apps/${app.id}/pin",
		"if (isRun || pinned) acts +=",
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("capability pinning is missing %q", required)
		}
	}
}
