package catalog

import "testing"

func TestHermesUsesOfficialLocalDashboard(t *testing.T) {
	hermes, ok := Get("hermes")
	if !ok {
		t.Fatal("Hermes is missing from the catalog")
	}
	if hermes.Image != "nousresearch/hermes-agent:v2026.7.20" {
		t.Fatalf("unexpected Hermes image: %q", hermes.Image)
	}
	if hermes.Network != "host" {
		t.Fatalf("Hermes network = %q, want host", hermes.Network)
	}
	if !hermes.Preinstall {
		t.Fatal("Hermes must be preinstalled as the core Cloudless agent")
	}
	if !hermes.LocalOnly || hermes.HasWebPort() || hermes.Tunnelable() {
		t.Fatal("Hermes dashboard must not be exposed by automatic sharing")
	}
	if hermes.DataPath != "/opt/data" || hermes.DataUID != 10000 {
		t.Fatalf("Hermes data mount = %q uid %d", hermes.DataPath, hermes.DataUID)
	}
	if hermes.Ports[HermesDashboardPort] != HermesDashboardPort || hermes.OpenPath != "/" {
		t.Fatal("Hermes dashboard is not configured on port 9119")
	}
	if hermes.Env["HERMES_DASHBOARD"] != "1" || hermes.Env["HERMES_DASHBOARD_HOST"] != "127.0.0.1" {
		t.Fatal("Hermes dashboard must be enabled on loopback")
	}
	if hermes.Env["API_SERVER_ENABLED"] != "true" || hermes.Env["API_SERVER_HOST"] != "127.0.0.1" {
		t.Fatal("Hermes API must be enabled on loopback for the authenticated Cloudless gateway")
	}
	if hermes.Env["HERMES_MAX_TOKENS"] != "4096" {
		t.Fatal("Hermes output must be capped independently from its context window")
	}
	if len(hermes.Command) != 2 || hermes.Command[0] != "gateway" || hermes.Command[1] != "run" {
		t.Fatalf("unexpected Hermes command: %#v", hermes.Command)
	}
}
