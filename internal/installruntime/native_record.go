package installruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

func installedNativeHash(bundleHash string, evidence []byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "managed-native-v1:%s:%d:", bundleHash, len(evidence))
	h.Write(evidence)
	return hex.EncodeToString(h.Sum(nil))
}

func validateNativeRecord(record *NativeRecord) error {
	if record == nil {
		return nil
	}
	if record.InstalledTreeSHA256 != installedNativeHash(record.SHA256, record.Attestation) {
		return fmt.Errorf("installed native attestation binding mismatch")
	}
	for _, gen := range record.Published {
		if gen.Path == "" || gen.SHA256 == "" || gen.InstalledTreeSHA256 != installedNativeHash(gen.SHA256, gen.Attestation) {
			return fmt.Errorf("published native generation binding mismatch")
		}
		if filepath.Dir(gen.Path) != filepath.Dir(record.Path) {
			return fmt.Errorf("published native generation escaped owner")
		}
	}
	return nil
}

func nativeGenerationPath(parent, directoryID string) string {
	return filepath.Join(parent, "generation-"+directoryID+".app")
}

func nativeGenerationOf(record NativeRecord) NativeGeneration {
	return NativeGeneration{
		DirectoryID: record.DirectoryID, Path: record.Path, SHA256: record.SHA256,
		DecoderFloor: record.DecoderFloor, Attestation: append([]byte(nil), record.Attestation...),
		InstalledTreeSHA256: record.InstalledTreeSHA256,
	}
}

func importNativeGenerations(record NativeRecord) []NativeGeneration {
	seen := map[string]bool{}
	var out []NativeGeneration
	add := func(gen NativeGeneration) {
		if gen.Path == "" || gen.SHA256 == "" || seen[gen.Path] {
			return
		}
		seen[gen.Path] = true
		out = append(out, gen)
	}
	// The live record is authoritative for the active generation, including
	// qualification evidence patched after the original Published snapshot.
	add(nativeGenerationOf(record))
	for _, gen := range record.Published {
		add(gen)
	}
	if record.PreviousPath != "" && record.PreviousSHA256 != "" {
		add(NativeGeneration{
			DirectoryID: record.PreviousDirectoryID, Path: record.PreviousPath, SHA256: record.PreviousSHA256,
			// The fallback has no retained decoder evidence; do not borrow
			// the floor of the newer active generation.
			InstalledTreeSHA256: installedNativeHash(record.PreviousSHA256, nil),
		})
	}
	return out
}

func nativeGenerationByDigest(record NativeRecord, digest string) *NativeGeneration {
	for _, gen := range importNativeGenerations(record) {
		if gen.SHA256 == digest {
			g := gen
			return &g
		}
	}
	return nil
}

func nativeRecordContainsPath(record NativeRecord, path string) bool {
	if record.Path == path || record.PreviousPath == path {
		return true
	}
	for _, gen := range record.Published {
		if gen.Path == path {
			return true
		}
	}
	return false
}

func ownedNativeAlias(link string, change *NativeChange) bool {
	if change == nil || link == "" {
		return false
	}
	parent := filepath.Dir(change.After.Path)
	if filepath.Dir(link) != parent {
		return false
	}
	return nativeRecordContainsPath(change.Before, link) || nativeRecordContainsPath(change.After, link)
}

func publishNativeRecord(before, after NativeRecord) NativeRecord {
	gens := importNativeGenerations(before)
	if match := nativeGenerationByDigest(NativeRecord{Published: gens}, after.SHA256); match != nil {
		after.Path = match.Path
		after.DirectoryID = match.DirectoryID
		// A historical generation may predate offline attestation; a
		// verified fresh attestation for identical bytes must survive reuse.
		if len(match.Attestation) != 0 {
			after.Attestation = append([]byte(nil), match.Attestation...)
			after.InstalledTreeSHA256 = match.InstalledTreeSHA256
		} else {
			after.InstalledTreeSHA256 = installedNativeHash(after.SHA256, after.Attestation)
		}
		if after.DecoderFloor < match.DecoderFloor {
			after.DecoderFloor = match.DecoderFloor
		}
		after.Published = gens
	} else {
		after.Published = append(gens, nativeGenerationOf(after))
	}
	if before.Path != "" && before.Path != after.Path {
		after.PreviousPath = before.Path
		after.PreviousSHA256 = before.SHA256
		after.PreviousDirectoryID = before.DirectoryID
	}
	return after
}
