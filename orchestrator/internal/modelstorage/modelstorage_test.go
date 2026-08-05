package modelstorage

import "testing"

func TestNFSConfigValidationAndIdentity(t *testing.T) {
	config := Config{Mode: ModeNFS, Server: "NAS.EXAMPLE.COM.", Export: "/cloudless/models", Version: "4.2", MarkerID: "0123456789abcdef0123456789abcdef"}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := config.Normalized().Server; got != "nas.example.com" {
		t.Fatalf("normalized server = %q", got)
	}
	if config.Identity() == (Config{Mode: ModeNFS, Server: "nas.example.com", Export: "/other", Version: "4.2", MarkerID: config.MarkerID}).Identity() {
		t.Fatal("storage identity ignored the export")
	}
	if MarkerContents(config) == "" {
		t.Fatal("shared marker is empty")
	}
}

func TestManagedNFSConfigUsesCoordinatorHub(t *testing.T) {
	config := Config{Mode: ModeNFS, Managed: true, Server: "10.100.0.1", Export: ManagedExport, Version: DefaultVersion, MarkerID: "0123456789abcdef0123456789abcdef"}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	config.Export = "/custom/export"
	if err := config.Validate(); err == nil {
		t.Fatal("managed storage accepted a user-selected export")
	}
}

func TestExternalNFSIdentityRemainsUpgradeCompatible(t *testing.T) {
	config := Config{Mode: ModeNFS, Server: "nas.example.com", Export: "/cloudless/models", Version: "4.2", MarkerID: "0123456789abcdef0123456789abcdef"}
	if got, want := config.Identity(), "sha256:fcfa3d77cc5df1f032a939d9ff05c1ae58851dea1781189d69ac1c017440725d"; got != want {
		t.Fatalf("external NFS identity changed across upgrade: got %s want %s", got, want)
	}
}

func TestNFSConfigRejectsUnsafeInputs(t *testing.T) {
	valid := Config{Mode: ModeNFS, Server: "10.0.0.8", Export: "/cloudless/models", Version: "4.2", MarkerID: "0123456789abcdef0123456789abcdef"}
	tests := []Config{
		{Mode: "smb"},
		withNFSField(valid, "server", "nas;reboot"),
		withNFSField(valid, "export", "/cloudless/../etc"),
		withNFSField(valid, "export", "/cloudless/models with spaces"),
		withNFSField(valid, "version", "3"),
		withNFSField(valid, "marker", "bad"),
	}
	for _, candidate := range tests {
		if err := candidate.Validate(); err == nil {
			t.Fatalf("unsafe config was accepted: %#v", candidate)
		}
	}
}

func withNFSField(config Config, field, value string) Config {
	switch field {
	case "server":
		config.Server = value
	case "export":
		config.Export = value
	case "version":
		config.Version = value
	case "marker":
		config.MarkerID = value
	}
	return config
}
