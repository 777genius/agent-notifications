//go:build linux && amd64

// TEST-only read-only measurement; process collection belongs to the guest driver.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/linuxcallback"
	"golang.org/x/sys/unix"
)

const fixtureRoot = "/var/lib/navigation-client-handoff-TEST/session"
const manifestSHA = "0d90a150c5066973b5884e2086b632737536bcbf362404ed81bb284d3b37f326"

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // Read-only diagnostic handle.
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err == nil && len(b) > 16384 {
		err = errors.New("diagnostic_size_bound")
	}
	return b, err
}

func bootTime() (float64, error) {
	var ts unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts)
	return float64(ts.Sec) + float64(ts.Nsec)/1e9, err
}

func ioCounters() (map[string]uint64, error) {
	b, err := readSmall("/proc/self/io")
	if err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	for _, row := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.Fields(row)
		if len(parts) != 2 {
			return nil, errors.New("invalid_proc_io")
		}
		n, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(parts[0], ":")] = n
	}
	return out, nil
}

func shape() (linuxcallback.Snapshot, error) {
	s, err := linuxcallback.SelectedSnapshot("/usr/lib/chatgpt")
	if err != nil || s.ManifestSHA256 != manifestSHA {
		return s, errors.New("authenticated_catalog_required")
	}
	info, err := os.Lstat(fixtureRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return s, errors.New("owned_TEST_session_required")
	}
	var rootStat unix.Stat_t
	if unix.Lstat(fixtureRoot, &rootStat) != nil || rootStat.Uid != 1000 {
		return s, errors.New("owned_TEST_session_required")
	}
	reader, err := os.Executable()
	if err != nil || reader != filepath.Join(fixtureRoot, "vendor-verify-probe") {
		return s, errors.New("fixed_TEST_probe_path_required")
	}
	fd, err := unix.Open(reader, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return s, err
	}
	f := os.NewFile(uintptr(fd), reader)
	var readerStat unix.Stat_t
	if unix.Fstat(fd, &readerStat) != nil || readerStat.Mode&unix.S_IFMT != unix.S_IFREG || readerStat.Uid != 1000 || readerStat.Mode&0022 != 0 {
		_ = f.Close()
		return s, errors.New("owned_regular_TEST_reader_required")
	}
	h := sha256.New()
	n, readErr := io.Copy(h, io.LimitReader(f, (32<<20)+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || n == 0 || n > 32<<20 {
		return s, errors.New("bounded_TEST_reader_required")
	}
	s.InstallationID = strings.Repeat("a", 64)
	s.ApplicationID = "org.agentnotifications.Callback" + s.InstallationID
	s.Reader, s.ReaderSHA256 = reader, hex.EncodeToString(h.Sum(nil))
	s.Records, s.DataRoot = fixtureRoot+"/measurement-records", fixtureRoot
	return s, nil // Shape only; no installed callback or registration is asserted.
}

func measure(out map[string]any) error {
	budgets := map[string]time.Duration{"cold90": 90 * time.Second, "warm15": 15 * time.Second, "warm3": 3 * time.Second, "cold3": 3 * time.Second}
	if len(os.Args) != 2 || budgets[os.Args[1]] == 0 {
		return errors.New("one_mode_required_cold90_warm15_warm3_cold3")
	}
	mode, budget := os.Args[1], budgets[os.Args[1]]
	groups, err := os.Getgroups()
	if err != nil || os.Getuid() != 1000 || os.Geteuid() != 1000 || os.Getgid() != 1000 || os.Getegid() != 1000 || len(groups) != 0 {
		return errors.New("actual_UID_GID1000_empty_groups_required")
	}
	out["mode"], out["requestedSeconds"], out["uid"], out["gid"], out["groups"] = mode, budget.Seconds(), os.Getuid(), os.Getgid(), groups
	out["cacheLabel"] = "operator_asserted_" + mode
	out["hostDeviceCacheState"] = "uncontrolled"
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("build_identity_required")
	}
	out["goVersion"] = build.GoVersion
	for _, setting := range build.Settings {
		if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" || setting.Key == "GOOS" || setting.Key == "GOARCH" {
			out[setting.Key] = setting.Value
		}
	}
	bootID, err := readSmall("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	pidStat, err := readSmall("/proc/self/stat")
	if err != nil {
		return err
	}
	end := strings.LastIndex(string(pidStat), ")")
	if end < 0 || len(strings.Fields(string(pidStat)[end+1:])) < 20 {
		return errors.New("invalid_process_identity")
	}
	out["bootID"], out["pid"], out["startTicks"] = strings.TrimSpace(string(bootID)), os.Getpid(), strings.Fields(string(pidStat)[end+1:])[19]
	s, err := shape()
	if err != nil {
		return err
	}
	out["readerSHA256"], out["manifestSHA256"], out["packageSHA256"], out["selectedRoot"] = s.ReaderSHA256, s.ManifestSHA256, s.ReleaseSHA256, "/usr/lib/chatgpt"
	out["declaredCatalogEntries"], out["declaredRegularFiles"], out["declaredRegularBytes"] = 4481, 3858, int64(1625716257)
	out["actualVerifierBytesRead"], out["actualVerifierEntriesVisited"] = nil, nil
	beforeIO, err := ioCounters()
	if err != nil {
		return err
	}
	var beforeCPU, afterCPU unix.Rusage
	if err = unix.Getrusage(unix.RUSAGE_SELF, &beforeCPU); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	beginBoot, err := bootTime()
	if err != nil {
		return err
	}
	start := time.Now()
	verifyErr := linuxcallback.CheckSelectedContext(ctx, s)
	elapsed := time.Since(start).Seconds()
	endBoot, clockErr := bootTime()
	contextErr := ctx.Err()
	out["verifierReturned"], out["beginBoot"], out["endBoot"], out["elapsedMonotonicSeconds"], out["elapsedBootSeconds"] = true, beginBoot, endBoot, elapsed, endBoot-beginBoot
	out["contextDeadlineExceeded"] = errors.Is(contextErr, context.DeadlineExceeded)
	out["contextCanceled"] = errors.Is(contextErr, context.Canceled)
	out["verifierPassed"] = verifyErr == nil
	if err = unix.Getrusage(unix.RUSAGE_SELF, &afterCPU); err != nil {
		return err
	}
	cpu := func(v unix.Timeval) float64 { return float64(v.Sec) + float64(v.Usec)/1e6 }
	out["userCPUSeconds"], out["systemCPUSeconds"], out["peakRSSKiB"] = cpu(afterCPU.Utime)-cpu(beforeCPU.Utime), cpu(afterCPU.Stime)-cpu(beforeCPU.Stime), afterCPU.Maxrss
	afterIO, err := ioCounters()
	if err != nil {
		return err
	}
	delta := map[string]uint64{}
	for key, n := range afterIO {
		if n < beforeIO[key] {
			return errors.New("proc_io_counter_decreased")
		}
		delta[key] = n - beforeIO[key]
	}
	out["procIOBefore"], out["procIOAfter"], out["procIODelta"] = beforeIO, afterIO, delta
	out["procIONote"] = "sampling affects rchar/syscr; read_bytes is storage IO, not bytes hashed"
	if verifyErr != nil {
		return verifyErr
	}
	if clockErr != nil || contextErr != nil || endBoot < beginBoot || endBoot-beginBoot >= budget.Seconds() || elapsed >= budget.Seconds() {
		return errors.New("verification_budget_not_proven")
	}
	return nil
}

func main() {
	out := map[string]any{"scope": "readonly_TEST_vendor_verifier_component", "passed": false, "verifierPassed": false, "externalProcessCollectionRequired": true, "snapshotShapeOnly": true, "nativeQualification": false}
	err := measure(out)
	code := 0
	if err != nil {
		code = 1
		message := err.Error()
		if len(message) > 512 {
			message = message[:512]
		}
		out["error"] = message
	} else {
		out["passed"] = true
	}
	out["intendedExitCode"] = code
	b, marshalErr := json.Marshal(out)
	if marshalErr != nil || len(b) > 32768 {
		b, code = []byte(`{"passed":false,"error":"result_encoding_bound","nativeQualification":false}`), 1
	}
	if _, err := fmt.Fprintln(os.Stdout, string(b)); err != nil {
		code = 1
	}
	os.Exit(code)
}
