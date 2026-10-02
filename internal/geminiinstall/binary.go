package geminiinstall

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func binaryName(goos, goarch string) (string, bool) {
	if (goos == "linux" || goos == "darwin") && (goarch == "amd64" || goarch == "arm64") {
		return "claude-notifications-" + goos + "-" + goarch, true
	}
	if goos == "windows" && goarch == "amd64" {
		return "claude-notifications-windows-amd64.exe", true
	}
	return "", false
}

func readBinary(path, goos, goarch string) ([]byte, uint32, error) {
	data, id, err := readBounded(path, 32<<20)
	if err != nil || !id.Exists || (goos != "windows" && id.Mode&0111 == 0) || !installruntime.WriterCompatible(data) {
		return nil, 0, errors.New("bounded executable with managed writer protocol required")
	}
	valid := false
	switch goos {
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(data))
		if err == nil {
			valid = f.Class == elf.ELFCLASS64 && ((goarch == "amd64" && f.Machine == elf.EM_X86_64) || (goarch == "arm64" && f.Machine == elf.EM_AARCH64))
			_ = f.Close()
		}
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(data))
		if err == nil {
			valid = (goarch == "amd64" && f.Cpu == macho.CpuAmd64) || (goarch == "arm64" && f.Cpu == macho.CpuArm64)
			_ = f.Close()
		}
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(data))
		if err == nil {
			valid = goarch == "amd64" && f.Machine == pe.IMAGE_FILE_MACHINE_AMD64
			_ = f.Close()
		}
	}
	if !valid {
		return nil, 0, errors.New("binary format or architecture does not match Gemini target")
	}
	if goos == "windows" {
		return data, 0600, nil
	}
	return data, id.Mode &^ 0022, nil
}
