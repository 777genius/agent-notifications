package opencodecodec

// Row data is separate from algorithm bytes. No semantic IDs are frozen here:
// root must first bind complete source/native evidence to this changed algorithm.
type ImageKey struct {
	GOOS, GOARCH, Entry, SHA256 string
}

type Candidate struct {
	Version, Generation string
}

// These are official image identities, not proof of an executing process.
var candidateImages = [...]struct {
	key       ImageKey
	candidate Candidate
}{
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, Candidate{"2.0.21", "v2"}},
}

type ClockRow struct {
	ProfileID, CalibrationID, Generation, RawKind            string
	NativeReadBoundNS, ComparisonBoundNS, TranslationBoundNS int64
}

// Intentionally empty. Sampled maxima and native helper module prequalification
// cannot populate this table. There is no public registration or grant setter.
var qualifiedClockRows = [...]struct {
	key ImageKey
	row ClockRow
}{}
