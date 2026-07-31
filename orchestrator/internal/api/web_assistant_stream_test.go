package api

import (
	"strings"
	"testing"
)

func TestAssistantStreamingMutatesOneMessageInsteadOfRerenderingThread(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(content)
	for _, want := range []string{
		"function updateAsstStream(mi)",
		"function queueAsstStreamUpdate(mi)",
		"requestAnimationFrame(() =>",
		"if (text) text.textContent = txt",
		"queueAsstStreamUpdate(botIndex)",
		"function finalizeAsstMessage(mi, m)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("incremental assistant streaming contract missing %q", want)
		}
	}
	streamStart := strings.Index(html, "async function asstSend(text)")
	if streamStart < 0 {
		t.Fatal("assistant streaming function boundaries missing")
	}
	loopStart := strings.Index(html[streamStart:], "for (;;) {")
	if loopStart < 0 {
		t.Fatal("assistant stream loop start missing")
	}
	loopEnd := strings.Index(html[streamStart+loopStart:], "} catch (err)")
	if loopEnd < 0 {
		t.Fatal("assistant stream loop end missing")
	}
	loop := html[streamStart+loopStart : streamStart+loopStart+loopEnd]
	if strings.Contains(loop, "asstRender(") || strings.Contains(loop, "innerHTML = asstMsgs") {
		t.Fatal("token streaming rebuilds the assistant thread")
	}
}
