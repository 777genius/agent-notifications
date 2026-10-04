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
	{ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "serve", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}, ClockRow{"", "linux-amd64-proc-boottime-v1:cc35d21daf67986bd1d681559493acc69f2df05ee3245973dbb05fb96606652d", "linux-amd64-proc-boottime-v1:cc35d21daf67986bd1d681559493acc69f2df05ee3245973dbb05fb96606652d:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "serve", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:92940f8dcf22997d46783d16d3b20f4a62b5c952dd23b0dd170e6754811b75f9", "linux-arm64-linux-proc-boottime-v1:92940f8dcf22997d46783d16d3b20f4a62b5c952dd23b0dd170e6754811b75f9:same-coordinate", "v2", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "serve", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:66efb18cf54eacc4836a513612f801616489842c62a1e90484ea8bf294141d1b", "darwin-amd64-darwin-mach-continuous-v1:66efb18cf54eacc4836a513612f801616489842c62a1e90484ea8bf294141d1b:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "serve", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:b3089b32ae2bc34f860ad5c8141589734fbebf267b4d1f74b2e9582b2caa9d30", "darwin-arm64-darwin-mach-continuous-v1:b3089b32ae2bc34f860ad5c8141589734fbebf267b4d1f74b2e9582b2caa9d30:same-coordinate", "v2", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "serve", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"}, ClockRow{"bounded", "windows-amd64-windows-interrupt-precise-v1:b00ff7e2996488a87022808db5b1328bd6196acce31d73babcfd71df938c31f6", "windows-amd64-windows-interrupt-precise-v1:b00ff7e2996488a87022808db5b1328bd6196acce31d73babcfd71df938c31f6:same-coordinate", "v2", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "tui", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "arm64", "run", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"}, ClockRow{"bounded", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd", "linux-arm64-linux-proc-boottime-v1:383d1878f482a5c871f1abd459ed4acf739988bd529ab3a3b001e0b15c8969bd:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "tui", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "amd64", "run", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"}, ClockRow{"bounded", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4", "darwin-amd64-darwin-mach-continuous-v1:d85bd97e29f4ded6fb5280ea12eb64d1a61009d51e1c4d87dae2f87b8b6cc9d4:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "tui", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"darwin", "arm64", "run", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"}, ClockRow{"bounded", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949", "darwin-arm64-darwin-mach-continuous-v1:229b4f960a54ba33f64f6b87ac4e62b375edd6c28d7cf08979654757cff4c949:same-coordinate", "v1", "darwin-monotonic-raw", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "run", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "tui", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"linux", "amd64", "run", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}, ClockRow{"", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09", "linux-amd64-proc-boottime-v1:f4d34f37994194998d73f96a56b9880a15865748ad80a98a85d311d5b6940c09:same-coordinate", "v1", "linux-boottime", 103000000, 430000000, 224000000}},
	{ImageKey{"windows", "amd64", "tui", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}, ClockRow{"unverified_original_date", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6", "windows-amd64-windows-interrupt-precise-v1:b32685bd7b43f1a5858e1b209e1e55b8c42569459bb94fd74ef744ff2e9c94a6:same-coordinate", "v1", "windows-interrupt-precise", 103000000, 430000000, 224000000}},
}
