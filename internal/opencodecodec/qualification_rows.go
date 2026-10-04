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
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:a8cbea2c6111502aad6067d2fbe9e2090fecea29fe05141b4c312381a15de319", "linux-amd64-proc-boottime-v1:a8cbea2c6111502aad6067d2fbe9e2090fecea29fe05141b4c312381a15de319:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:60531a6a0f25f2b3b7d0792f0305dbefa2c07646aebe036f98ff391bd845fcbd", "linux-arm64-linux-proc-boottime-v1:60531a6a0f25f2b3b7d0792f0305dbefa2c07646aebe036f98ff391bd845fcbd:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:8229c0a658e025814a2e69a81444d88bad7da17ddb9a8c8dfc2275d33381aaf0", "darwin-amd64-darwin-mach-continuous-v1:8229c0a658e025814a2e69a81444d88bad7da17ddb9a8c8dfc2275d33381aaf0:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:47aa852fc2e3dd9817a4978ec81345392c0c0a95ae18c80b4b218ac22fb61907", "darwin-arm64-darwin-mach-continuous-v1:47aa852fc2e3dd9817a4978ec81345392c0c0a95ae18c80b4b218ac22fb61907:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:6dd58be2045bf8ff734a9768f26022075e74370b01ef9835dde3910e8b35fd59", "windows-amd64-windows-interrupt-precise-v1:6dd58be2045bf8ff734a9768f26022075e74370b01ef9835dde3910e8b35fd59:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a", "linux-arm64-linux-proc-boottime-v1:dbc2a4d4a2dbc2b236e0a545adbdba64e96921efc2900b8a77b9064812115e2a:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c", "darwin-amd64-darwin-mach-continuous-v1:c52332bd6f91d5b5bb761eb74f4a6824779f5d0566f75a81ce3fc687e2e8687c:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304", "darwin-arm64-darwin-mach-continuous-v1:d450e99e754a7a97502bada71ef7e510e8b2014b3cb10d3005dc8ada5b127304:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0", "linux-amd64-proc-boottime-v1:e98eda97811872b4e8962541a783b7c5c683ed361439fb9e2ac1ff5898ab3bd0:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9", "windows-amd64-windows-interrupt-precise-v1:17560a83ff993f65523b05707eb4f763118f3dbd7a6b88705dd794ef88b8c4c9:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
