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
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:76cc2c10e6ea859c2ba5337c68bde589b77d81ddb40d5690a978852efff1446d", "linux-amd64-proc-boottime-v1:76cc2c10e6ea859c2ba5337c68bde589b77d81ddb40d5690a978852efff1446d:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:964ee4614f5d75a16e10443154da67e96a1ec4e12d82929d5493432bdbe3d2f7", "linux-arm64-linux-proc-boottime-v1:964ee4614f5d75a16e10443154da67e96a1ec4e12d82929d5493432bdbe3d2f7:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:e6f27879e5bf78db47dace1422faa9a49373c279440060628880b60465046c3f", "darwin-amd64-darwin-mach-continuous-v1:e6f27879e5bf78db47dace1422faa9a49373c279440060628880b60465046c3f:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:40b6a9aa0ed937e59ade8a44d34fafda0cbd80e4907f0eae32a67e1a504e66eb", "darwin-arm64-darwin-mach-continuous-v1:40b6a9aa0ed937e59ade8a44d34fafda0cbd80e4907f0eae32a67e1a504e66eb:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:a2b963cf88d61ebc2c5975ac702bda096d3f016cbab194ee71496ac5ee801535", "windows-amd64-windows-interrupt-precise-v1:a2b963cf88d61ebc2c5975ac702bda096d3f016cbab194ee71496ac5ee801535:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	// SOURCE staging: existing physical IDs must be rerendered against final roots.
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2", "linux-arm64-linux-proc-boottime-v1:9ef4819e18831287e9241bd3d7a08f73f29827ec46874ae259250feb0d1b9ad2:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae", "darwin-amd64-darwin-mach-continuous-v1:da0ae41042f263acbd71b7b4d0a5e090d54f6e69fe8108be51fd597609e034ae:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be", "darwin-arm64-darwin-mach-continuous-v1:30aef2fa937a52a61057794ffe3caacf871c0cc6573d4a7068099f5cfbe993be:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17", "linux-amd64-proc-boottime-v1:0424fa1959123a00b45acd835344a99e2cd877bb59e20071e5a939db03b11e17:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9", "windows-amd64-windows-interrupt-precise-v1:25203d7ce432f3d3dd770f34bcc2abb5f4d4550e9ce475761b32d0cf5e7b50f9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
