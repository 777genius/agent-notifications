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
	// Exact accepted local-entry SOURCE keys; no Windows TUI inheritance.
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, Candidate{"1.18.34", "v1"}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, Candidate{"1.18.33", "v1"}},
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
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:1f8de0e6e46c96853e7438984ba15b2c919514da4576cfce5b6f6e8dfbf49695", "linux-amd64-proc-boottime-v1:1f8de0e6e46c96853e7438984ba15b2c919514da4576cfce5b6f6e8dfbf49695:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:0352a341ee9cec774f44c232756e4e865e060ad757f3e64ffdb58f131a2fec48", "linux-arm64-linux-proc-boottime-v1:0352a341ee9cec774f44c232756e4e865e060ad757f3e64ffdb58f131a2fec48:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:3f77c3a337b7e6e94aafb491726e113f1f89a69ab818de32cc88e54f2c0e4d1d", "darwin-amd64-darwin-mach-continuous-v1:3f77c3a337b7e6e94aafb491726e113f1f89a69ab818de32cc88e54f2c0e4d1d:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:fcb9027521deedddf4fdd8c37b70b8bff63592f99934efce34023f37b00f373e", "darwin-arm64-darwin-mach-continuous-v1:fcb9027521deedddf4fdd8c37b70b8bff63592f99934efce34023f37b00f373e:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:6cd05f2c0f42673a406477c64c3755aac80710fc6c78fd8b5773b0cb137a1991", "windows-amd64-windows-interrupt-precise-v1:6cd05f2c0f42673a406477c64c3755aac80710fc6c78fd8b5773b0cb137a1991:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d", "linux-arm64-linux-proc-boottime-v1:370c1b74da0709b8278c30f219a35c111d810899e414e95f4c0d2052fa416f9d:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f", "darwin-amd64-darwin-mach-continuous-v1:ea758a63449bc00a42ee821ab5b5f4901f5bc0debb7cdec9f604ecd50c23af5f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f", "darwin-arm64-darwin-mach-continuous-v1:c27858a9e71bd9a05d05ecd82cfd7682847387797ac77693e9801b39a514c85f:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e", "linux-amd64-proc-boottime-v1:34ddee7a12767598230daad5ff392103b39ea34ef131f730173162ebb70b1f9e:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0", "windows-amd64-windows-interrupt-precise-v1:02b12bf2f4016f1d4a2111dbda007dd512d48adbd400faf81482b07d9f493ee0:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
