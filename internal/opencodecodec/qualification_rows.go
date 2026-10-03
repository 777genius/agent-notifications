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
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, Candidate{"2.0.21", "v2"}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, Candidate{"2.0.21", "v2"}},
}

type ClockRow struct {
	OriginalNativeAge                                        string
	ProfileID, CalibrationID, Generation, RawKind            string
	NativeReadBoundNS, ComparisonBoundNS, TranslationBoundNS int64
}

// Reviewed native/source prerequisites are compiled here. Sampled maxima alone
// cannot populate this table. There is no public registration or grant setter.
var qualifiedClockRows = [...]struct {
	key ImageKey
	row ClockRow
}{
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c474e3010a7a7902a78a4b6a5658753820265c436f53b092e366b2c9693edde3", "linux-amd64-proc-boottime-v1:c474e3010a7a7902a78a4b6a5658753820265c436f53b092e366b2c9693edde3:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:c474e3010a7a7902a78a4b6a5658753820265c436f53b092e366b2c9693edde3", "linux-amd64-proc-boottime-v1:c474e3010a7a7902a78a4b6a5658753820265c436f53b092e366b2c9693edde3:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:b8a98b4f3340b97f72c160583fa5cb5e7238b2e26e2cc5ae8347827722acad5a", "linux-amd64-proc-boottime-v1:b8a98b4f3340b97f72c160583fa5cb5e7238b2e26e2cc5ae8347827722acad5a:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:b0852fba328c070b0dfbe671aeac14f50607af045529e1253c2776c43fbdd22a", "linux-arm64-linux-proc-boottime-v1:b0852fba328c070b0dfbe671aeac14f50607af045529e1253c2776c43fbdd22a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:ebadbdf45c22111ceac5ccfcc1bb42a4b53f0f2c5dd7e9749290c560e3e4caf6", "linux-arm64-linux-proc-boottime-v1:ebadbdf45c22111ceac5ccfcc1bb42a4b53f0f2c5dd7e9749290c560e3e4caf6:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:ceada0c605e39eb212dbe7c8e2fb354ae91d39d7fdf1557ffcb0a3a2825fe54e", "darwin-amd64-darwin-mach-continuous-v1:ceada0c605e39eb212dbe7c8e2fb354ae91d39d7fdf1557ffcb0a3a2825fe54e:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:2917d10ea6474c6412a83644b24043bf243416ab386e3f27956227c77dd98fd6", "darwin-amd64-darwin-mach-continuous-v1:2917d10ea6474c6412a83644b24043bf243416ab386e3f27956227c77dd98fd6:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:9043a3e07e6fab03484818fa728c591f34611295e645c7f74fdb251090a6646f", "darwin-arm64-darwin-mach-continuous-v1:9043a3e07e6fab03484818fa728c591f34611295e645c7f74fdb251090a6646f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:5397a247affafc98054c2b16cec8ec503970b53d3371594e74748241581d319e", "darwin-arm64-darwin-mach-continuous-v1:5397a247affafc98054c2b16cec8ec503970b53d3371594e74748241581d319e:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:5bb9571e398cfcebb405ddfc07c726f82a69b89b1a410b7d155a17dd2c0dcab7", "windows-amd64-windows-interrupt-precise-v1:5bb9571e398cfcebb405ddfc07c726f82a69b89b1a410b7d155a17dd2c0dcab7:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:5d29f9d1eacd18960c6909d8f7e02a98c7f53d6a0516587fcc57963b5fb797e3", "windows-amd64-windows-interrupt-precise-v1:5d29f9d1eacd18960c6909d8f7e02a98c7f53d6a0516587fcc57963b5fb797e3:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
