package main

import (
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestDebugCodeIsShortLivedAndSingleUse(t *testing.T) {
	now := time.Now()
	var c debugCode
	if c.verify("", now) {
		t.Fatal("accepted a missing code")
	}
	value, err := c.issue(now)
	if err != nil || len(value) != 8 || strings.Trim(value, "0123456789") != "" {
		t.Fatalf("could not generate 8 digit code: %v", err)
	}
	if !c.verify(value, now) || c.verify(value, now) {
		t.Fatal("code must succeed once only")
	}
	value, _ = c.issue(now)
	if c.verify(value, now.Add(debugCodeTTL)) {
		t.Fatal("expired code accepted")
	}
	value, _ = c.issue(now)
	for i := 0; i < 5; i++ {
		if c.verify("0000000-", now) {
			t.Fatal("incorrect code accepted")
		}
	}
	if c.verify(value, now) {
		t.Fatal("correct code accepted after the retry limit")
	}
}

func TestDesktopBridgeAllowsOnlyMainWorkspacePage(t *testing.T) {
	for _, source := range []string{desktopPage, desktopPage + "#/tasks"} {
		if !desktopFrame(&application.OriginInfo{Origin: source, TopOrigin: desktopPage, IsMainFrame: true}) {
			t.Errorf("rejected workbench %q", source)
		}
	}
	for _, source := range []string{"", "https://example.test/desktop", localOrigin, desktopPage + "?x=1", "wails://localhost/sandbox/index.html", "http://127.0.0.1:8787/desktop", "http://localhost:8787/desk%74op"} {
		if desktopFrame(&application.OriginInfo{Origin: source, IsMainFrame: true}) {
			t.Errorf("accepted foreign URL %q", source)
		}
	}
	if desktopFrame(nil) || desktopFrame(&application.OriginInfo{Origin: desktopPage}) || desktopFrame(&application.OriginInfo{Origin: desktopPage, TopOrigin: "https://example.test", IsMainFrame: true}) {
		t.Fatal("accepted unverified frame")
	}
}
