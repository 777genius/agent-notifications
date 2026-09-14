package portableasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFetchDownloadsVerifiedZip(t *testing.T) {
	probe := buildProbe(t)
	base := t.TempDir()
	pkg := filepath.Join(base, "pkg")
	archive := filepath.Join(base, AssetName(runtime.GOOS, runtime.GOARCH))
	built, err := Build(BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: probe, OutputRoot: pkg, Archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.43.0/checksums.txt":
			_, _ = io.WriteString(w, built.ArchiveSHA256+"  "+asset+"\n")
		case "/v1.43.0/" + asset:
			_, _ = w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(base, "acquired")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := Fetch(context.Background(), FetchRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		DestParent: dest, DownloadRoot: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLayout(root); err != nil {
		t.Fatal(err)
	}
}

func TestFetchRejectsChecksumMismatchAndMissingAsset(t *testing.T) {
	asset := AssetName("linux", "amd64")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.43.0/checksums.txt":
			_, _ = io.WriteString(w, strings.Repeat("a", 64)+"  "+asset+"\n")
		case "/v1.43.0/" + asset:
			_, _ = io.WriteString(w, "not-a-zip")
		case "/v9.9.9/checksums.txt":
			_, _ = io.WriteString(w, strings.Repeat("b", 64)+"  other.zip\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	dest := t.TempDir()
	_, err := Fetch(ctx, FetchRequest{
		Version: "1.43.0", GOOS: "linux", GOARCH: "amd64",
		DestParent: dest, DownloadRoot: srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("mismatch: %v", err)
	}
	_, err = Fetch(ctx, FetchRequest{
		Version: "9.9.9", GOOS: "linux", GOARCH: "amd64",
		DestParent: dest, DownloadRoot: srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("missing: %v", err)
	}
}

func TestHTTPGetRejectsNonHTTP(t *testing.T) {
	_, err := HTTPGet(context.Background(), "file:///etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("file: %v", err)
	}
}

func TestChecksumForBinaryMode(t *testing.T) {
	sum := sha256.Sum256([]byte("zip"))
	digest := hex.EncodeToString(sum[:])
	got, err := checksumFor([]byte(digest+" *agent-notify-portable-linux-amd64.zip\n"), "agent-notify-portable-linux-amd64.zip")
	if err != nil || got != digest {
		t.Fatalf("%s %v", got, err)
	}
}
