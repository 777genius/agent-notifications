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

// Reviewed native/source prerequisites are compiled here. Sampled maxima alone
// cannot populate this table. There is no public registration or grant setter.
var qualifiedClockRows = [...]struct {
	key ImageKey
	row ClockRow
}{
	{ImageKey{"linux","amd64","serve","0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"linux-amd64-proc-boottime-v1:800e64510a17f657bce10b87fdbf7f1dbbb21ab7b5a772418e78037dd981e1a7","linux-amd64-proc-boottime-v1:800e64510a17f657bce10b87fdbf7f1dbbb21ab7b5a772418e78037dd981e1a7:same-coordinate","v1","linux-boottime",103000000,430000000,224000000}},
	{ImageKey{"linux","amd64","serve","9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"linux-amd64-proc-boottime-v1:800e64510a17f657bce10b87fdbf7f1dbbb21ab7b5a772418e78037dd981e1a7","linux-amd64-proc-boottime-v1:800e64510a17f657bce10b87fdbf7f1dbbb21ab7b5a772418e78037dd981e1a7:same-coordinate","v1","linux-boottime",103000000,430000000,224000000}},
	{ImageKey{"linux","amd64","serve","f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"linux-amd64-proc-boottime-v1:0f82ab50ddb64f8161796a869ce0891f193e3f3be2e55681721f804fb6b3c3cc","linux-amd64-proc-boottime-v1:0f82ab50ddb64f8161796a869ce0891f193e3f3be2e55681721f804fb6b3c3cc:same-coordinate","v2","linux-boottime",103000000,430000000,224000000}},
}
