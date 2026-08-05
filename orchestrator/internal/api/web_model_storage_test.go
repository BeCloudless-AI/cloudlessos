package api

import (
	"strings"
	"testing"
)

func TestClusterUIExposesValidatedSharedNFSStorage(t *testing.T) {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(page)
	for _, expected := range []string{
		"Shared NFS model storage", "cluster-nfs-toggle", "openClusterNFSStorage", "openClusterLocalStorage",
		"/api/settings/model-storage/nfs", "/api/settings/model-storage/local",
		"skip weight transfer", "Unload Cloudless AI before changing storage",
		"waitForModelStorageRefresh", "consistent UID/GID mapping", "fabric-restricted", "no_root_squash",
		"compiled caches stay local to each Spark", "share only the Hugging Face model hub",
		"cluster-storage-heading", "cluster-storage-fields", "cluster-storage-warning", "cluster-storage-summary",
		"grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(108px, .58fr)",
		".nfs-storage-dialog #key-dialog-content", "overflow: hidden", "max-width: 100%", "nfs-storage-dialog",
		"Share model weights from this Spark", "No server address or folder setup is required", "{managed:true}",
		"advancedInterfaceEnabled()", "Use a different NFS server", "Set up and verify",
		"if (!response.ok)", "payload?.error", "throw failure",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("shared NFS interface is missing %q", expected)
		}
	}
}
