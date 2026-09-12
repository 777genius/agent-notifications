//go:build darwin

package installruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const lsTools = `
#include <CoreServices/CoreServices.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
int register_app(const char *path) {
  CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, (CFIndex)strlen(path), true);
  if (!url) return 3;
  OSStatus result = LSRegisterURL(url, true);
  CFRelease(url);
  return result == noErr ? 0 : 1;
}
int query_app(const char *bundleID, const char *want, int dump) {
  char wantReal[4096];
  if (realpath(want, wantReal) == NULL) strlcpy(wantReal, want, sizeof(wantReal));
  CFStringRef identifier = CFStringCreateWithCString(NULL, bundleID, kCFStringEncodingUTF8);
  if (!identifier) return 3;
  CFArrayRef urls = LSCopyApplicationURLsForBundleIdentifier(identifier, NULL);
  CFRelease(identifier);
  if (!urls) {
    if (dump) fprintf(stderr, "ls: no URLs for %s want=%s\n", bundleID, wantReal);
    return 1;
  }
  int found = 1;
  CFIndex n = CFArrayGetCount(urls);
  for (CFIndex i = 0; i < n; i++) {
    CFURLRef url = CFArrayGetValueAtIndex(urls, i);
    char path[4096];
    if (!CFURLGetFileSystemRepresentation(url, true, (UInt8 *)path, sizeof(path))) continue;
    char gotReal[4096];
    if (realpath(path, gotReal) == NULL) strlcpy(gotReal, path, sizeof(gotReal));
    if (dump) fprintf(stderr, "ls: %s\n", gotReal);
    if (strcmp(gotReal, wantReal) == 0) found = 0;
  }
  CFRelease(urls);
  return found;
}
int main(int argc, char **argv) {
  if (argc == 3 && strcmp(argv[1], "register") == 0) return register_app(argv[2]);
  if (argc == 4 && strcmp(argv[1], "query") == 0) return query_app(argv[2], argv[3], 0);
  if (argc == 4 && strcmp(argv[1], "dump") == 0) return query_app(argv[2], argv[3], 1);
  return 2;
}
`

func registerPublishedNative(t *testing.T, tool, bundle, id string) {
	t.Helper()
	if out, err := exec.Command(tool, "register", bundle).CombinedOutput(); err != nil {
		t.Fatalf("register: %s %v", out, err)
	}
	lsregister := "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	if _, err := os.Stat(lsregister); err == nil {
		_ = exec.Command(lsregister, "-f", bundle).Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	var last []byte
	for {
		out, err := exec.Command(tool, "query", id, bundle).CombinedOutput()
		if err == nil {
			return
		}
		last = out
		if time.Now().After(deadline) {
			dump, _ := exec.Command(tool, "dump", id, bundle).CombinedOutput()
			if strings.Contains(string(dump), "no URLs") {
				t.Skipf("LaunchServices did not index unique test bundle %s (LSRegisterURL is inert here); generation identity is covered by exact-head tests; queued click remains a manual gate. dump=%s", id, dump)
			}
			t.Fatalf("query %s: %s %v dump=%s", bundle, last, err, dump)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestNativePublishedGenerationRemainsRegisteredAfterUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	_, r := request(t)
	id := "com.claude.desktop.notifier.test." + filepath.Base(t.TempDir())
	tool := filepath.Join(t.TempDir(), "ls-tool")
	src := filepath.Join(t.TempDir(), "ls-tool.c")
	if err := os.WriteFile(src, []byte(lsTools), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-o", tool, src, "-framework", "CoreServices").CombinedOutput(); err != nil {
		t.Skipf("core services tool: %s %v", out, err)
	}
	assemble := func(marker string) string {
		t.Helper()
		root := exactHeadNativeApp(t, marker)
		plist := filepath.Join(root, "Contents", "Info.plist")
		body, err := os.ReadFile(plist)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(body), "com.claude.desktop.notifier", id, 1)
		updated = strings.Replace(updated, "2.0.0", marker, 1)
		if err := os.WriteFile(plist, []byte(updated), 0644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("codesign", "--force", "--sign", "-", "--timestamp=none", root).CombinedOutput(); err != nil {
			t.Fatalf("codesign: %s %v", out, err)
		}
		return root
	}
	change, err := StageNative(ctx, r.ControlRoot, assemble("generation-A"))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	pathA := change.After.Path
	plist, err := os.ReadFile(filepath.Join(pathA, "Contents", "Info.plist"))
	if err != nil || !strings.Contains(string(plist), id) {
		t.Fatalf("published generation A lost test bundle id: %s", plist)
	}
	registerPublishedNative(t, tool, pathA, id)
	change, err = StageNative(ctx, r.ControlRoot, assemble("generation-B"))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pathA); err != nil {
		t.Fatal("generation A disappeared")
	}
	registerPublishedNative(t, tool, change.After.Path, id)
	if out, err := exec.Command(tool, "query", id, pathA).CombinedOutput(); err != nil {
		dump, _ := exec.Command(tool, "dump", id, pathA).CombinedOutput()
		t.Fatalf("LaunchServices lost generation A after update: %s dump=%s", out, dump)
	}
}
